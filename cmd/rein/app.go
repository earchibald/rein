package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	"github.com/earchibald/rein/internal/dashboards"
	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/reporoot"
	"github.com/earchibald/rein/internal/server"
	"github.com/earchibald/rein/internal/service"
	"github.com/earchibald/rein/internal/storage/sqlite"
	"github.com/earchibald/rein/internal/telemetry"
	"github.com/earchibald/rein/internal/tui"
	protofiles "github.com/earchibald/rein/proto"
)

const defaultRPCTimeout = 10 * time.Second

var (
	loadRPCCommandsOnce  sync.Once
	cachedRPCCommands    map[string]rpcCommand
	cachedRPCCommandsErr error
)

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
	case "backup":
		return a.runBackup(root, remaining[1:])
	case "dashboards":
		return a.runDashboards(remaining[1:])
	case "doctor":
		return a.runDoctor(root, remaining[1:])
	case "describe-as":
		return a.runDescribe(remaining)
	case "restore":
		return a.runRestore(root, remaining[1:])
	case "tui":
		return a.runTUI(root, remaining[1:])
	case "version":
		return a.runVersion(remaining[1:])
	default:
		if strings.HasPrefix(remaining[0], "describe-as=") {
			return a.runDescribe(remaining)
		}
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

	serveConfig, err := parseDaemonServeConfig(root, args[1:], a.stderr, a.lookupEnv)
	if err != nil {
		return err
	}
	return a.serveDaemon(serveConfig)
}

func (a *app) runDescribe(args []string) error {
	format, showHelp, err := parseDescribeArgs(args)
	if err != nil {
		return err
	}
	if showHelp {
		a.printDescribeHelp()
		return flag.ErrHelp
	}

	output, err := renderDescribeAs(format)
	if err != nil {
		return err
	}
	_, err = io.WriteString(a.stdout, output)
	return err
}

func (a *app) serveDaemon(config daemonServeConfig) error {
	telemetryConfig := config.telemetry
	if telemetryConfig.ResourceAttributes == nil {
		telemetryConfig.ResourceAttributes = map[string]string{}
	}
	telemetryConfig.ResourceAttributes["rein.instance"] = config.instance.Name
	telemetryConfig.ResourceAttributes["service.instance.id"] = config.instance.Name
	daemonTelemetry, err := telemetry.NewDaemonRuntime(context.Background(), telemetryConfig, a.stdout)
	if err != nil {
		return fmt.Errorf("configure OTLP telemetry: %w", err)
	}
	defer func() {
		if shutdownErr := daemonTelemetry.Shutdown(context.Background()); shutdownErr != nil {
			_, _ = fmt.Fprintf(a.stderr, "rein: shutdown telemetry: %v\n", shutdownErr)
		}
	}()

	logger := daemonTelemetry.Logger
	if err := config.instance.EnsureRootDir(); err != nil {
		return fmt.Errorf("prepare instance state directory: %w", err)
	}
	pidFile, err := instance.AcquirePIDFile(config.instance.PIDPath)
	if err != nil {
		return fmt.Errorf("lock instance daemon pid file: %w", err)
	}
	defer pidFile.Close()

	store, err := sqlite.OpenAndMigrate(context.Background(), sqlite.Config{Path: config.instance.DatabasePath})
	if err != nil {
		return fmt.Errorf("open instance database: %w", err)
	}
	defer store.Close()

	catalog, err := a.loadManagedCatalog()
	if err != nil {
		return err
	}

	listenerConfig := config.listener
	listenerConfig.Logger = logger
	listener, err := server.Listen(listenerConfig)
	if err != nil {
		return fmt.Errorf("create listener: %w", err)
	}
	defer listener.Close()

	runtime := server.New(server.Options{
		Services:    service.NewManagedSet(store, catalog),
		GRPCOptions: daemonTelemetry.GRPCOptions,
	})
	daemonTelemetry.RecordStartup(context.Background())
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

func (a *app) loadManagedCatalog() (service.ManagedCatalog, error) {
	catalog, err := service.NewManagedCatalogFromRoot(".", adapter.LocalDiscoveryOptions())
	if err == nil {
		return catalog, nil
	}

	var notFound *reporoot.NotFoundError
	if errors.As(err, &notFound) {
		_, _ = fmt.Fprintf(a.stderr, "rein: warning: %v; continuing with built-in adapters only\n", err)
		return service.NewManagedCatalog(nil), nil
	}

	return nil, fmt.Errorf("load adapter registry: %w", err)
}

type dashboardsApplyConfig struct {
	Plugin   string
	BaseURL  string
	APIKey   string
	RootPath string
}

type dashboardsApplyOutput struct {
	Plugin     string   `json:"plugin"`
	Provider   string   `json:"provider"`
	Repository string   `json:"repositoryRoot"`
	CreatedIDs []string `json:"createdIds"`
	UpdatedIDs []string `json:"updatedIds"`
	SkippedIDs []string `json:"skippedIds"`
}

func (a *app) runDashboards(args []string) error {
	if len(args) == 0 || (len(args) == 1 && isHelpToken(args[0])) {
		a.printDashboardsHelp()
		return flag.ErrHelp
	}
	if args[0] != "apply" {
		return fmt.Errorf("unknown dashboards command %q", args[0])
	}

	config, err := parseDashboardsApplyConfig(args[1:], a.stderr, a.lookupEnv, a.getwd)
	if err != nil {
		return err
	}
	return a.applyDashboards(config)
}

func (a *app) applyDashboards(config dashboardsApplyConfig) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := dashboards.Apply(ctx, config.RootPath, dashboards.ApplyOptions{
		Plugin:  config.Plugin,
		BaseURL: config.BaseURL,
		APIKey:  config.APIKey,
	})
	if err != nil {
		return err
	}
	return writeJSONObject(a.stdout, dashboardsApplyOutput{
		Plugin:     result.Plugin,
		Provider:   result.Provider,
		Repository: config.RootPath,
		CreatedIDs: result.Created,
		UpdatedIDs: result.Updated,
		SkippedIDs: result.Skipped,
	})
}

func (a *app) runTUI(root rootConfig, args []string) error {
	if len(args) == 1 && isHelpToken(args[0]) {
		a.printTUIHelp()
		return flag.ErrHelp
	}
	if len(args) != 0 {
		return fmt.Errorf("unexpected arguments: %v", args)
	}

	ctx, cancel := context.WithTimeout(context.Background(), defaultRPCTimeout)
	defer cancel()

	conn, err := dialGRPC(ctx, root.client)
	if err != nil {
		return fmt.Errorf("connect to daemon: %w", err)
	}
	defer conn.Close()

	return tui.Run(conn, tui.Options{
		InstanceName: root.instance.Name,
		Network:      root.client.Network,
		Address:      root.client.Address,
	})
}

func (a *app) runRPC(root rootConfig, args []string) error {
	commands, err := loadRPCCommands()
	if err != nil {
		return err
	}
	grouped := groupRPCCommandsByNoun(commands)
	if len(args) == 0 {
		a.printRootHelp()
		return flag.ErrHelp
	}
	if len(args) == 1 {
		if serviceCommands, ok := grouped[args[0]]; ok {
			a.printServiceHelp(args[0], serviceCommands)
			return flag.ErrHelp
		}
		a.printRootHelp()
		return flag.ErrHelp
	}
	if isHelpToken(args[1]) {
		if serviceCommands, ok := grouped[args[0]]; ok {
			a.printServiceHelp(args[0], serviceCommands)
			return flag.ErrHelp
		}
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
	loadRPCCommandsOnce.Do(func() {
		cachedRPCCommands, cachedRPCCommandsErr = loadRPCCommandsUncached()
	})
	return cachedRPCCommands, cachedRPCCommandsErr
}

func loadRPCCommandsUncached() (map[string]rpcCommand, error) {
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

func groupRPCCommandsByNoun(commands map[string]rpcCommand) map[string][]rpcCommand {
	grouped := make(map[string][]rpcCommand, len(commands))
	for _, command := range commands {
		grouped[command.noun] = append(grouped[command.noun], command)
	}
	for noun := range grouped {
		sort.Slice(grouped[noun], func(i, j int) bool {
			return grouped[noun][i].verb < grouped[noun][j].verb
		})
	}
	return grouped
}

func methodVerb(name protoreflect.Name) (string, bool) {
	for _, prefix := range []string{"List", "Get", "Inspect", "Create", "Update", "Start", "Cancel", "Validate"} {
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
	for _, line := range rootUsageLines() {
		fmt.Fprintf(a.stderr, "  %s\n", line)
	}
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Global flags:")
	for _, flag := range globalFlagDescriptions() {
		fmt.Fprintf(a.stderr, "  --%s %s\n    %s\n", flag.Name, flag.Type, flag.Description)
	}
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Commands:")

	grouped := groupRPCCommandsByNoun(commands)
	nouns := make([]string, 0, len(grouped))
	for noun := range grouped {
		nouns = append(nouns, noun)
	}
	sort.Strings(nouns)
	for _, noun := range nouns {
		commandsForNoun := grouped[noun]
		fmt.Fprintf(a.stderr, "  %s\t%s\n", noun, cleanComment(commentText(commandsForNoun[0].service)))
		for _, command := range commandsForNoun {
			fmt.Fprintf(a.stderr, "    %s %s\t%s\n", noun, command.verb, cleanComment(commentText(command.method)))
		}
	}
	fmt.Fprintln(a.stderr)
	for _, utility := range utilityCommandDescriptions() {
		command := utility.Command
		if utility.Name == "describe_as" {
			command = "describe-as=<format>"
		}
		fmt.Fprintf(a.stderr, "  %s\t%s\n", command, utility.Summary)
	}
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Help:")
	fmt.Fprintln(a.stderr, "  rein <service> --help\tList verbs for a service group.")
	fmt.Fprintln(a.stderr, "  rein <service> <verb> --help\tShow flags for a specific RPC command.")
}

func (a *app) printBackupHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] backup [flags] <destination>")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Checkpoint SQLite WAL for the selected instance and atomically copy its")
	fmt.Fprintln(a.stderr, "state directory into <destination>. Runtime sockets and daemon pid files")
	fmt.Fprintln(a.stderr, "are skipped. <destination> must not already exist.")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Flags:")
	fmt.Fprintln(a.stderr, "  --stop bool")
	fmt.Fprintln(a.stderr, "    Stop the selected daemon first as a paranoid fallback.")
}

func (a *app) printDaemonHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] daemon serve [flags]")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Flags:")
	for _, flag := range daemonServeFlagDescriptions() {
		fmt.Fprintf(a.stderr, "  --%s %s\n    %s\n", flag.Name, flag.Type, flag.Description)
	}
}

func (a *app) printDashboardsHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein dashboards apply [flags]")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Apply the reference rein-dashboards plugin to a SigNoz API endpoint.")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Flags:")
	fmt.Fprintln(a.stderr, "  --plugin string")
	fmt.Fprintln(a.stderr, "    Dashboards plugin name (default \"rein-dashboards\").")
	fmt.Fprintln(a.stderr, "  --signoz-url string")
	fmt.Fprintln(a.stderr, "    SigNoz base URL (or SIGNOZ_BASE_URL / SIGNOZ_URL).")
	fmt.Fprintln(a.stderr, "  --signoz-api-key string")
	fmt.Fprintln(a.stderr, "    SigNoz API key (or SIGNOZ_API_KEY).")
}

func (a *app) printDescribeHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] describe-as=<format>")
	fmt.Fprintln(a.stderr, "  rein [global flags] describe-as <format>")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Formats:")
	for _, format := range supportedDescribeFormats() {
		fmt.Fprintf(a.stderr, "  %s\n    %s\n", format.Name, format.Description)
	}
}

func (a *app) printDoctorHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] doctor")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Emit machine-parseable JSON diagnostics covering daemon reachability,")
	fmt.Fprintln(a.stderr, "instance layout, adapter registry compatibility, credential readiness,")
	fmt.Fprintln(a.stderr, "and SQLite migration state for the selected instance.")
}

func (a *app) printRestoreHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] restore [flags] <source>")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Atomically replace the selected instance state directory from <source>.")
	fmt.Fprintln(a.stderr, "The daemon must be stopped first; pass --stop to stop a daemon that still")
	fmt.Fprintln(a.stderr, "holds the selected instance pid file. Runtime sockets and daemon pid files")
	fmt.Fprintln(a.stderr, "from the backup are not restored.")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Flags:")
	fmt.Fprintln(a.stderr, "  --stop bool")
	fmt.Fprintln(a.stderr, "    Stop the selected daemon before replacing its state directory.")
}

func (a *app) printTUIHelp() {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintln(a.stderr, "  rein [global flags] tui")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Launch the terminal UI over the daemon's canonical gRPC API.")
	fmt.Fprintln(a.stderr)
	fmt.Fprintln(a.stderr, "Keys:")
	fmt.Fprintln(a.stderr, "  tab / shift+tab  Change focus between projects, issues, and executions.")
	fmt.Fprintln(a.stderr, "  up/down          Move the selected row in the focused list.")
	fmt.Fprintln(a.stderr, "  pgup/pgdown      Scroll the overview/drilldown pane when it overflows.")
	fmt.Fprintln(a.stderr, "  home/end         Jump to the top or bottom of the overview/drilldown pane.")
	fmt.Fprintln(a.stderr, "  enter            Toggle compact vs expanded execution drilldown.")
	fmt.Fprintln(a.stderr, "  r                Refresh daemon data.")
	fmt.Fprintln(a.stderr, "  q                Quit.")
}

func (a *app) printServiceHelp(noun string, commands []rpcCommand) {
	fmt.Fprintln(a.stderr, "Usage:")
	fmt.Fprintf(a.stderr, "  rein [global flags] %s <verb> [flags]\n", noun)
	fmt.Fprintf(a.stderr, "  rein [global flags] %s --help\n", noun)
	fmt.Fprintln(a.stderr)
	if len(commands) > 0 {
		fmt.Fprintln(a.stderr, cleanComment(commentText(commands[0].service)))
		fmt.Fprintln(a.stderr)
	}
	fmt.Fprintln(a.stderr, "Verbs:")
	for _, command := range commands {
		fmt.Fprintf(a.stderr, "  %s\t%s\n", command.verb, cleanComment(commentText(command.method)))
	}
	fmt.Fprintln(a.stderr)
	fmt.Fprintf(a.stderr, "Use \"rein [global flags] %s <verb> --help\" for verb-specific flags.\n", noun)
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
	if (field.IsList() || field.IsMap() || field.Kind() == protoreflect.MessageKind) && !strings.Contains(strings.ToLower(comment), "json") {
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

func parseDescribeArgs(args []string) (format string, showHelp bool, err error) {
	if len(args) == 0 {
		return "", false, fmt.Errorf("describe-as format is required")
	}
	if strings.HasPrefix(args[0], "describe-as=") {
		format = strings.TrimPrefix(args[0], "describe-as=")
		if format == "" {
			return "", false, fmt.Errorf("describe-as format is required")
		}
		if len(args) == 1 {
			return format, false, nil
		}
		if len(args) == 2 && isHelpToken(args[1]) {
			return "", true, nil
		}
		return "", false, fmt.Errorf("unexpected arguments: %v", args[1:])
	}
	if args[0] != "describe-as" {
		return "", false, fmt.Errorf("unknown describe command %q", args[0])
	}
	if len(args) == 1 {
		return "", true, nil
	}
	if isHelpToken(args[1]) {
		return "", true, nil
	}
	if len(args) != 2 {
		return "", false, fmt.Errorf("unexpected arguments: %v", args[2:])
	}
	return args[1], false, nil
}

func sliceMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
