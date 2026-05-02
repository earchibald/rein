package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/earchibald/rein/internal/adapter"
	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
	"github.com/earchibald/rein/internal/service"
	"github.com/earchibald/rein/internal/storage/sqlite"
	protofiles "github.com/earchibald/rein/proto"
)

const defaultRPCTimeout = 10 * time.Second

type app struct {
	stdout      io.Writer
	stderr      io.Writer
	lookupEnv   func(string) (string, bool)
	userHomeDir func() (string, error)
	getwd       func() (string, error)
}

type rpcCommand struct {
	noun       string
	verb       string
	service    protoreflect.ServiceDescriptor
	method     protoreflect.MethodDescriptor
	input      protoreflect.MessageDescriptor
	output     protoreflect.MessageDescriptor
	fullMethod string
}

type fieldBinding struct {
	field protoreflect.FieldDescriptor
	raw   string
	set   bool
}

func newApp(stdout, stderr io.Writer, lookupEnv func(string) (string, bool), userHomeDir, getwd func() (string, error)) *app {
	if getwd == nil {
		getwd = os.Getwd
	}
	return &app{
		stdout:      stdout,
		stderr:      stderr,
		lookupEnv:   lookupEnv,
		userHomeDir: userHomeDir,
		getwd:       getwd,
	}
}

func (a *app) run(args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		a.printRootHelp()
		return flag.ErrHelp
	}

	root, remaining, err := parseRootConfig(args, a.stderr, a.lookupEnv, a.userHomeDir)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		a.printRootHelp()
		return flag.ErrHelp
	}

	switch remaining[0] {
	case "daemon":
		return a.runDaemon(root, remaining[1:])
	case "doctor":
		return a.runDoctor(root, remaining[1:])
	default:
		return a.runRPC(root, remaining)
	}
}

func (a *app) runDaemon(root rootConfig, args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelpToken(args[0])) {
		a.printDaemonHelp()
		return flag.ErrHelp
	}
	if args[0] != "serve" {
		return fmt.Errorf("unknown daemon command %q", args[0])
	}

	serveConfig, err := parseDaemonServeConfig(root, args[1:], a.stderr)
	if err != nil {
		return err
	}
	return a.serveDaemon(serveConfig)
}

func (a *app) serveDaemon(config daemonServeConfig) error {
	logger := slog.New(slog.NewTextHandler(a.stdout, nil))
	if err := config.instance.EnsureRootDir(); err != nil {
		return fmt.Errorf("prepare instance state directory: %w", err)
	}

	store, err := sqlite.OpenAndMigrate(context.Background(), sqlite.Config{Path: config.instance.DatabasePath})
	if err != nil {
		return fmt.Errorf("open instance database: %w", err)
	}
	defer store.Close()

	catalog, err := service.NewManagedCatalogFromRoot(".", adapter.DiscoveryOptions{})
	if err != nil {
		return fmt.Errorf("load adapter registry: %w", err)
	}

	listenerConfig := config.listener
	listenerConfig.Logger = logger
	listener, err := server.Listen(listenerConfig)
	if err != nil {
		return fmt.Errorf("create listener: %w", err)
	}
	defer listener.Close()

	runtime := server.New(server.Options{Services: service.NewManagedSet(store, catalog)})
	logger.Info(
		"rein daemon starting",
		"instance", config.instance.Name,
		"state_dir", config.instance.RootDir,
		"database", config.instance.DatabasePath,
		"network", listenerConfig.Network,
		"address", listener.Addr().String(),
		"auto_start", config.instance.AutoStartEnabled(),
	)
	logger.Info(
		"rein HTTP/SSE gateway v2 stub ready",
		"routes", len(runtime.Gateway().Routes()),
		"streams", len(runtime.Gateway().Streams()),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runtime.Serve(ctx, listener)
}

func (a *app) runRPC(root rootConfig, args []string) error {
	commands, err := loadRPCCommands()
	if err != nil {
		return err
	}
	if len(args) < 2 {
		a.printRootHelp()
		return flag.ErrHelp
	}

	command, ok := commands[args[0]+" "+args[1]]
	if !ok {
		return fmt.Errorf("unknown command %q", strings.Join(args[:sliceMin(len(args), 2)], " "))
	}
	if len(args) > 2 && isHelpToken(args[2]) {
		a.printCommandHelp(command)
		return flag.ErrHelp
	}

	flagSet := flag.NewFlagSet("rein "+command.noun+" "+command.verb, flag.ContinueOnError)
	flagSet.SetOutput(a.stderr)
	flagSet.Usage = func() {
		a.printCommandHelp(command)
	}

	bindings := make([]*fieldBinding, 0, command.input.Fields().Len())
	for i := 0; i < command.input.Fields().Len(); i++ {
		field := command.input.Fields().Get(i)
		binding := &fieldBinding{field: field}
		flagSet.Var(binding, string(field.Name()), fieldUsage(field))
		bindings = append(bindings, binding)
	}

	if err := flagSet.Parse(args[2:]); err != nil {
		return err
	}
	if flagSet.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flagSet.Args())
	}

	request, err := buildRequest(command.input, bindings)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()

	conn, err := dialGRPC(ctx, root.client)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	response := dynamicpb.NewMessage(command.output)
	if err := conn.Invoke(ctx, command.fullMethod, request, response); err != nil {
		return err
	}

	return writeJSON(a.stdout, response)
}

func dialGRPC(_ context.Context, config clientConfig) (*grpc.ClientConn, error) {
	target := "passthrough:///" + config.Address
	return grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, config.Network, config.Address)
		}),
	)
}

func loadRPCCommands() (map[string]rpcCommand, error) {
	services := []struct {
		fullName protoreflect.FullName
		noun     string
	}{
		{fullName: "rein.v1.ProjectService", noun: "project"},
		{fullName: "rein.v1.IssueService", noun: "issue"},
		{fullName: "rein.v1.ExecutionService", noun: "execution"},
		{fullName: "rein.v1.WorkflowService", noun: "workflow"},
		{fullName: "rein.v1.AdapterService", noun: "adapter"},
	}

	commands := make(map[string]rpcCommand, 16)
	for _, entry := range services {
		descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName(entry.fullName)
		if err != nil {
			return nil, err
		}
		serviceDescriptor, ok := descriptor.(protoreflect.ServiceDescriptor)
		if !ok {
			return nil, fmt.Errorf("%s is not a service descriptor", entry.fullName)
		}

		for i := 0; i < serviceDescriptor.Methods().Len(); i++ {
			method := serviceDescriptor.Methods().Get(i)
			verb, ok := methodVerb(method.Name())
			if !ok {
				continue
			}
			command := rpcCommand{
				noun:       entry.noun,
				verb:       verb,
				service:    serviceDescriptor,
				method:     method,
				input:      method.Input(),
				output:     method.Output(),
				fullMethod: "/" + string(serviceDescriptor.FullName()) + "/" + string(method.Name()),
			}
			commands[command.noun+" "+command.verb] = command
		}
	}
	return commands, nil
}

func methodVerb(name protoreflect.Name) (string, bool) {
	for _, prefix := range []string{"List", "Get", "Create", "Update", "Start", "Cancel", "Validate"} {
		if strings.HasPrefix(string(name), prefix) {
			return strings.ToLower(prefix), true
		}
	}
	return "", false
}

func buildRequest(descriptor protoreflect.MessageDescriptor, bindings []*fieldBinding) (proto.Message, error) {
	values := map[string]json.RawMessage{}
	for _, binding := range bindings {
		if !binding.set {
			continue
		}
		token, err := binding.jsonValue()
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", binding.field.Name(), err)
		}
		values[binding.field.JSONName()] = token
	}

	payload, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}

	request := dynamicpb.NewMessage(descriptor)
	if err := protojson.Unmarshal(payload, request); err != nil {
		return nil, err
	}
	return request, nil
}

func writeJSON(w io.Writer, message proto.Message) error {
	data, err := protojson.MarshalOptions{
		Indent:        "  ",
		Multiline:     true,
		UseProtoNames: true,
	}.Marshal(message)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s\n", data); err != nil {
		return err
	}
	return nil
}

func (b *fieldBinding) String() string {
	return b.raw
}

func (b *fieldBinding) Set(value string) error {
	b.raw = value
	b.set = true
	return nil
}

func (b *fieldBinding) IsBoolFlag() bool {
	return !b.field.IsList() && !b.field.IsMap() && b.field.Kind() == protoreflect.BoolKind
}

func (b *fieldBinding) jsonValue() (json.RawMessage, error) {
	switch {
	case b.field.IsList(), b.field.IsMap(), b.field.Kind() == protoreflect.MessageKind:
		if !json.Valid([]byte(b.raw)) {
			return nil, fmt.Errorf("value must be valid JSON")
		}
		return json.RawMessage(b.raw), nil
	case b.field.Kind() == protoreflect.StringKind:
		encoded, _ := json.Marshal(b.raw)
		return encoded, nil
	case b.field.Kind() == protoreflect.BoolKind:
		value, err := strconv.ParseBool(b.raw)
		if err != nil {
			return nil, err
		}
		if value {
			return json.RawMessage("true"), nil
		}
		return json.RawMessage("false"), nil
	case b.field.Kind() == protoreflect.EnumKind:
		if _, err := strconv.Atoi(b.raw); err == nil {
			return json.RawMessage(b.raw), nil
		}
		encoded, _ := json.Marshal(b.raw)
		return encoded, nil
	case isIntegerKind(b.field.Kind()):
		if _, err := strconv.ParseInt(b.raw, 10, 64); err != nil {
			return nil, err
		}
		return json.RawMessage(b.raw), nil
	case isUnsignedKind(b.field.Kind()):
		if _, err := strconv.ParseUint(b.raw, 10, 64); err != nil {
			return nil, err
		}
		return json.RawMessage(b.raw), nil
	case b.field.Kind() == protoreflect.FloatKind || b.field.Kind() == protoreflect.DoubleKind:
		if _, err := strconv.ParseFloat(b.raw, 64); err != nil {
			return nil, err
		}
		return json.RawMessage(b.raw), nil
	default:
		encoded, _ := json.Marshal(b.raw)
		return encoded, nil
	}
}

func isIntegerKind(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind, protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return true
	default:
		return false
	}
}

func isUnsignedKind(kind protoreflect.Kind) bool {
	switch kind {
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind, protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return true
	default:
		return false
	}
}

func (a *app) printRootHelp() {
	commands, err := loadRPCCommands()
	if err != nil {
		fmt.Fprintf(a.stderr, "failed to load command descriptors: %v\n", err)
		return
	}

	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] <service> <verb> [flags]")
	fmt.Fprintln(a.stderr, "  rein [global flags] daemon serve [flags]")
	fmt.Fprintln(a.stderr, "  rein [global flags] doctor")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Global flags:")
	fmt.Fprintf(a.stderr, "  --instance string\n    Select the daemon instance (default %q or %s)\n", instance.DefaultName, instance.EnvVar)
	fmt.Fprintln(a.stderr, "  --grpc-network string")
	fmt.Fprintln(a.stderr, "    Override the daemon network for client connections.")
	fmt.Fprintln(a.stderr, "  --grpc-address string")
	fmt.Fprintln(a.stderr, "    Override the daemon address for client connections.")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Commands:")

	grouped := map[string][]rpcCommand{}
	for _, command := range commands {
		grouped[command.noun] = append(grouped[command.noun], command)
	}
	nouns := make([]string, 0, len(grouped))
	for noun := range grouped {
		nouns = append(nouns, noun)
	}
	sort.Strings(nouns)
	for _, noun := range nouns {
		commandsForNoun := grouped[noun]
		sort.Slice(commandsForNoun, func(i, j int) bool { return commandsForNoun[i].verb < commandsForNoun[j].verb })
		fmt.Fprintf(a.stderr, "  %s\t%s\n", noun, cleanComment(commentText(commandsForNoun[0].service)))
		for _, command := range commandsForNoun {
			fmt.Fprintf(a.stderr, "    %s %s\t%s\n", noun, command.verb, cleanComment(commentText(command.method)))
		}
	}
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "  daemon serve\tStart the daemon and expose the canonical gRPC surface.")
	fmt.Fprintln(a.stderr, "  doctor\tEmit JSON diagnostics for daemon health and local instance readiness.")
}

func (a *app) printDaemonHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] daemon serve [flags]")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Flags:")
	fmt.Fprintf(a.stderr, "  --grpc-network string\n    Listener network: tcp or unix (default %q)\n", server.DefaultListenerNetwork())
	fmt.Fprintln(a.stderr, "  --grpc-address string")
	fmt.Fprintln(a.stderr, "    Listener address or unix socket path.")
	fmt.Fprintf(a.stderr, "  --grpc-require-peer-credentials bool\n    Require SO_PEERCRED same-UID authentication for unix sockets (default %t)\n", server.DefaultListenerConfig().RequirePeerCredentials)
}

func (a *app) printDoctorHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] doctor")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Emit machine-parseable JSON diagnostics covering daemon reachability,")
	fmt.Fprintln(a.stderr, "instance layout, adapter registry compatibility, credential readiness,")
	fmt.Fprintln(a.stderr, "and SQLite migration state for the selected instance.")
}

func (a *app) printCommandHelp(command rpcCommand) {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintf(a.stderr, "  rein [global flags] %s %s", command.noun, command.verb)
	for i := 0; i < command.input.Fields().Len(); i++ {
		field := command.input.Fields().Get(i)
		fmt.Fprintf(a.stderr, " [--%s <%s>]", field.Name(), fieldTypeLabel(field))
	}
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr)
	if comment := cleanComment(commentText(command.method)); comment != "" {
		fmt.Fprintln(a.stderr, comment)
		fmt.Fprintln(a.stderr)
	}
	fmt.Fprintln(a.stderr, "Flags:")
	for i := 0; i < command.input.Fields().Len(); i++ {
		field := command.input.Fields().Get(i)
		fmt.Fprintf(a.stderr, "  --%s %s\n    %s\n", field.Name(), fieldTypeLabel(field), fieldUsage(field))
	}
}

func fieldUsage(field protoreflect.FieldDescriptor) string {
	comment := cleanComment(commentText(field))
	if comment == "" {
		comment = fmt.Sprintf("Set request field %s.", field.Name())
	}
	if field.IsList() || field.IsMap() || field.Kind() == protoreflect.MessageKind {
		return comment + " Pass JSON."
	}
	return comment
}

func fieldTypeLabel(field protoreflect.FieldDescriptor) string {
	switch {
	case field.IsList(), field.IsMap(), field.Kind() == protoreflect.MessageKind:
		return "json"
	case field.Kind() == protoreflect.BoolKind:
		return "bool"
	case field.Kind() == protoreflect.EnumKind:
		return "enum"
	case field.Kind() == protoreflect.StringKind:
		return "string"
	case isIntegerKind(field.Kind()), isUnsignedKind(field.Kind()):
		return "int"
	case field.Kind() == protoreflect.FloatKind || field.Kind() == protoreflect.DoubleKind:
		return "number"
	default:
		return "value"
	}
}

func commentText(descriptor protoreflect.Descriptor) string {
	if comment := protofiles.Comment(string(descriptor.FullName())); comment != "" {
		return comment
	}
	location := descriptor.ParentFile().SourceLocations().ByDescriptor(descriptor)
	return strings.TrimSpace(location.LeadingComments)
}

func cleanComment(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.Join(strings.Fields(value), " ")
}

func isHelpToken(value string) bool {
	return value == "-h" || value == "--help" || value == "help"
}

func sliceMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
