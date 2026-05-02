package main

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/earchibald/rein/internal/gateway"
	"github.com/earchibald/rein/internal/instance"
	"github.com/earchibald/rein/internal/server"
)

const (
	describeFormatCLI     = "cli"
	describeFormatMCP     = "mcp"
	describeFormatMCPFull = "mcp-full"
)

type describeSurface struct {
	Title           string
	Summary         string
	Conventions     []string
	Usage           []string
	Formats         []describeFormatDescription
	GlobalFlags     []staticFlagDescription
	Services        []serviceGroupDescription
	Commands        []commandDescription
	UtilityCommands []utilityCommandDescription
	Gateway         gatewayDescription
	SchemaIndex     schemaIndexDescription
	Messages        []messageSchemaDescription
	Enums           []enumSchemaDescription
}

type describeFormatDescription struct {
	Name        string
	Description string
}

type staticFlagDescription struct {
	Name        string
	Type        string
	Description string
}

type staticArgumentDescription struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

type serviceGroupDescription struct {
	Name    string
	CLI     string
	HelpCLI string
	Summary string
	Verbs   []serviceVerbDescription
}

type serviceVerbDescription struct {
	Name    string
	CLI     string
	Summary string
}

type utilityCommandDescription struct {
	Name      string
	Command   string
	CLI       string
	Summary   string
	Flags     []staticFlagDescription
	Arguments []staticArgumentDescription
}

type commandDescription struct {
	Name           string
	CLI            string
	Noun           string
	Verb           string
	Service        string
	ServiceSummary string
	Method         string
	Summary        string
	FullMethod     string
	RequestType    string
	ResponseType   string
	Flags          []fieldDescription
}

type fieldDescription struct {
	Name        string
	Type        string
	ProtoType   string
	SchemaRef   string
	Description string
	AcceptsJSON bool
	Repeated    bool
	Map         bool
	EnumValues  []enumValueDescription
}

type gatewayDescription struct {
	Routes  []gateway.Route
	Streams []gateway.Stream
}

type schemaIndexDescription struct {
	DetailFormat  string
	DetailCommand string
	MessageCount  int
	EnumCount     int
	Messages      []namedSchemaDescription
	Enums         []namedSchemaDescription
}

type namedSchemaDescription struct {
	Name        string
	Description string
}

type messageSchemaDescription struct {
	Name        string
	Description string
	Fields      []fieldDescription
}

type enumSchemaDescription struct {
	Name        string
	Description string
	Values      []enumValueDescription
}

type enumValueDescription struct {
	Name        string
	Number      int32
	Description string
}

var (
	compactDescribeSurfaceOnce sync.Once
	compactDescribeSurface     describeSurface
	compactDescribeSurfaceErr  error

	fullDescribeSurfaceOnce sync.Once
	fullDescribeSurface     describeSurface
	fullDescribeSurfaceErr  error
)

func supportedDescribeFormats() []describeFormatDescription {
	return []describeFormatDescription{
		{Name: describeFormatCLI, Description: "Manual-style description of RPC service groups, top-level utility commands, gateway routes, and reachable protobuf schemas."},
		{Name: describeFormatMCP, Description: "Compact YAML reference for wrapper and skill tooling with command/help discovery, gateway routes, and a schema index. Use mcp-full for the exhaustive schema dump."},
		{Name: describeFormatMCPFull, Description: "Exhaustive YAML surface including the detailed protobuf schema dump used by cli."},
	}
}

func rootUsageLines() []string {
	return []string{
		"rein [global flags] <service> <verb> [flags]",
		"rein [global flags] backup [flags] <destination>",
		"rein [global flags] daemon serve [flags]",
		"rein dashboards apply [flags]",
		"rein [global flags] doctor",
		"rein [global flags] describe-as=<format>",
		"rein [global flags] restore [flags] <source>",
		"rein [global flags] tui",
		"rein version [--json]",
	}
}

func rootConventions() []string {
	return []string{
		"Request flags map 1:1 to top-level gRPC request fields.",
		"Scalar fields take plain values; message, repeated, and map fields take JSON blobs.",
		"Responses are emitted as JSON using protobuf field names.",
		"Use 'rein <service> --help' for subgroup help and 'rein <service> <verb> --help' for command flags.",
	}
}

func globalFlagDescriptions() []staticFlagDescription {
	return []staticFlagDescription{
		{Name: "instance", Type: "string", Description: fmt.Sprintf("Select the daemon instance (default %q or %s).", instance.DefaultName, instance.EnvVar)},
		{Name: "grpc-network", Type: "string", Description: "Override the daemon network for client connections."},
		{Name: "grpc-address", Type: "string", Description: "Override the daemon address for client connections."},
	}
}

func daemonServeFlagDescriptions() []staticFlagDescription {
	defaults := server.DefaultListenerConfig()
	return []staticFlagDescription{
		{Name: "grpc-network", Type: "string", Description: fmt.Sprintf("Listener network: tcp or unix (default %q).", defaults.Network)},
		{Name: "grpc-address", Type: "string", Description: "Listener address or unix socket path."},
		{Name: "grpc-require-peer-credentials", Type: "bool", Description: fmt.Sprintf("Require SO_PEERCRED same-UID authentication for unix sockets (default %t).", defaults.RequirePeerCredentials)},
		{Name: "otlp-endpoint", Type: "string", Description: "Optional OTLP/gRPC collector endpoint (host:port)."},
		{Name: "otlp-headers", Type: "string", Description: "Optional OTLP headers as comma-separated key=value pairs."},
		{Name: "otlp-insecure", Type: "bool", Description: "Disable TLS for OTLP collector connections."},
	}
}

func utilityCommandDescriptions() []utilityCommandDescription {
	return []utilityCommandDescription{
		{
			Name:    "backup",
			Command: "backup",
			CLI:     "rein [global flags] backup [flags] <destination>",
			Summary: "Checkpoint SQLite WAL and atomically copy the selected instance state.",
			Flags: []staticFlagDescription{{
				Name:        "stop",
				Type:        "bool",
				Description: "Stop the selected daemon first as a paranoid fallback.",
			}},
			Arguments: []staticArgumentDescription{{
				Name:        "destination",
				Type:        "path",
				Description: "Destination directory that must not already exist.",
				Required:    true,
			}},
		},
		{
			Name:    "daemon_serve",
			Command: "daemon serve",
			CLI:     "rein [global flags] daemon serve [flags]",
			Summary: "Start the daemon and expose the canonical gRPC surface.",
			Flags:   daemonServeFlagDescriptions(),
		},
		{
			Name:    "dashboards_apply",
			Command: "dashboards apply",
			CLI:     "rein dashboards apply [flags]",
			Summary: "Apply reference OTLP dashboards to a SigNoz workspace.",
			Flags: []staticFlagDescription{
				{Name: "plugin", Type: "string", Description: "Dashboards plugin name (default \"rein-dashboards\")."},
				{Name: "signoz-url", Type: "string", Description: "SigNoz base URL (or SIGNOZ_BASE_URL / SIGNOZ_URL)."},
				{Name: "signoz-api-key", Type: "string", Description: "SigNoz API key (or SIGNOZ_API_KEY)."},
			},
		},
		{
			Name:    "doctor",
			Command: "doctor",
			CLI:     "rein [global flags] doctor",
			Summary: "Emit JSON diagnostics for daemon health and local instance readiness.",
		},
		{
			Name:    "describe_as",
			Command: "describe-as",
			CLI:     "rein [global flags] describe-as=<format>",
			Summary: "Emit a stable machine-consumable surface description.",
			Arguments: []staticArgumentDescription{{
				Name:        "format",
				Type:        "string",
				Description: fmt.Sprintf("Supported formats: %s.", supportedDescribeFormatNames()),
				Required:    true,
			}},
		},
		{
			Name:    "restore",
			Command: "restore",
			CLI:     "rein [global flags] restore [flags] <source>",
			Summary: "Atomically replace the selected instance state from a backup copy.",
			Flags: []staticFlagDescription{{
				Name:        "stop",
				Type:        "bool",
				Description: "Stop the selected daemon before replacing its state directory.",
			}},
			Arguments: []staticArgumentDescription{{
				Name:        "source",
				Type:        "path",
				Description: "Backup directory to restore from.",
				Required:    true,
			}},
		},
		{
			Name:    "tui",
			Command: "tui",
			CLI:     "rein [global flags] tui",
			Summary: "Terminal UI over the canonical gRPC surface.",
		},
		{
			Name:    "version",
			Command: "version",
			CLI:     "rein version [--json]",
			Summary: "Print the CLI version and embedded build provenance.",
			Flags: []staticFlagDescription{{
				Name:        "json",
				Type:        "bool",
				Description: "Emit structured JSON.",
			}},
		},
	}
}

func buildDescribeSurfaceCompact() (describeSurface, error) {
	compactDescribeSurfaceOnce.Do(func() {
		compactDescribeSurface, compactDescribeSurfaceErr = constructDescribeSurface(false)
	})
	return compactDescribeSurface, compactDescribeSurfaceErr
}

func buildDescribeSurfaceFull() (describeSurface, error) {
	fullDescribeSurfaceOnce.Do(func() {
		fullDescribeSurface, fullDescribeSurfaceErr = constructDescribeSurface(true)
	})
	return fullDescribeSurface, fullDescribeSurfaceErr
}

func constructDescribeSurface(includeSchemas bool) (describeSurface, error) {
	commandsByKey, err := loadRPCCommands()
	if err != nil {
		return describeSurface{}, err
	}

	collector := newSchemaCollector()
	commands := make([]commandDescription, 0, len(commandsByKey))
	for _, command := range commandsByKey {
		commands = append(commands, describeCommand(command))
		collector.addMessage(command.input)
		collector.addMessage(command.output)
	}
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Noun != commands[j].Noun {
			return commands[i].Noun < commands[j].Noun
		}
		return commands[i].Verb < commands[j].Verb
	})

	gatewayStub := gateway.NewV2Stub()
	surface := describeSurface{
		Title:           "rein",
		Summary:         "Descriptor-driven reference for the rein CLI, gRPC services, and v2 gateway stub.",
		Conventions:     rootConventions(),
		Usage:           rootUsageLines(),
		Formats:         supportedDescribeFormats(),
		GlobalFlags:     globalFlagDescriptions(),
		Services:        describeServiceGroups(commandsByKey),
		Commands:        commands,
		UtilityCommands: utilityCommandDescriptions(),
		Gateway:         gatewayDescription{Routes: gatewayStub.Routes(), Streams: gatewayStub.Streams()},
		SchemaIndex:     collector.schemaIndex(),
	}
	if includeSchemas {
		surface.Messages = collector.messageSchemas()
		surface.Enums = collector.enumSchemas()
	}
	return surface, nil
}

func describeServiceGroups(commandsByKey map[string]rpcCommand) []serviceGroupDescription {
	grouped := groupRPCCommandsByNoun(commandsByKey)
	nouns := make([]string, 0, len(grouped))
	for noun := range grouped {
		nouns = append(nouns, noun)
	}
	sort.Strings(nouns)

	services := make([]serviceGroupDescription, 0, len(nouns))
	for _, noun := range nouns {
		commands := grouped[noun]
		verbs := make([]serviceVerbDescription, 0, len(commands))
		for _, command := range commands {
			verbs = append(verbs, serviceVerbDescription{
				Name:    command.verb,
				CLI:     fmt.Sprintf("rein [global flags] %s %s [flags]", command.noun, command.verb),
				Summary: cleanComment(commentText(command.method)),
			})
		}
		services = append(services, serviceGroupDescription{
			Name:    noun,
			CLI:     fmt.Sprintf("rein [global flags] %s <verb> [flags]", noun),
			HelpCLI: fmt.Sprintf("rein [global flags] %s --help", noun),
			Summary: cleanComment(commentText(commands[0].service)),
			Verbs:   verbs,
		})
	}
	return services
}

func describeCommand(command rpcCommand) commandDescription {
	flags := make([]fieldDescription, 0, command.input.Fields().Len())
	for i := 0; i < command.input.Fields().Len(); i++ {
		flags = append(flags, describeField(command.input.Fields().Get(i)))
	}
	return commandDescription{
		Name:           command.noun + "_" + command.verb,
		CLI:            fmt.Sprintf("rein [global flags] %s %s [flags]", command.noun, command.verb),
		Noun:           command.noun,
		Verb:           command.verb,
		Service:        string(command.service.FullName()),
		ServiceSummary: cleanComment(commentText(command.service)),
		Method:         string(command.method.Name()),
		Summary:        cleanComment(commentText(command.method)),
		FullMethod:     command.fullMethod,
		RequestType:    string(command.input.FullName()),
		ResponseType:   string(command.output.FullName()),
		Flags:          flags,
	}
}

func describeField(field protoreflect.FieldDescriptor) fieldDescription {
	description := fieldUsage(field)
	schemaRef := ""
	if field.IsMap() {
		if field.MapValue().Kind() == protoreflect.MessageKind {
			schemaRef = string(field.MapValue().Message().FullName())
		} else if field.MapValue().Kind() == protoreflect.EnumKind {
			schemaRef = string(field.MapValue().Enum().FullName())
		}
	} else {
		switch field.Kind() {
		case protoreflect.MessageKind:
			schemaRef = string(field.Message().FullName())
		case protoreflect.EnumKind:
			schemaRef = string(field.Enum().FullName())
		default:
			// Scalar fields do not reference nested schemas.
		}
	}

	var enumValues []enumValueDescription
	if field.IsMap() {
		if field.MapValue().Kind() == protoreflect.EnumKind {
			enumValues = describeEnumValues(field.MapValue().Enum())
		}
	} else if field.Kind() == protoreflect.EnumKind {
		enumValues = describeEnumValues(field.Enum())
	}

	return fieldDescription{
		Name:        string(field.Name()),
		Type:        fieldTypeLabel(field),
		ProtoType:   protoTypeLabel(field),
		SchemaRef:   schemaRef,
		Description: description,
		AcceptsJSON: field.IsList() || field.IsMap() || field.Kind() == protoreflect.MessageKind,
		Repeated:    field.IsList(),
		Map:         field.IsMap(),
		EnumValues:  enumValues,
	}
}

func protoTypeLabel(field protoreflect.FieldDescriptor) string {
	if field.IsMap() {
		return fmt.Sprintf("map<%s, %s>", scalarKindLabel(field.MapKey().Kind()), nestedFieldBaseType(field.MapValue()))
	}
	base := nestedFieldBaseType(field)
	if field.IsList() {
		return "repeated " + base
	}
	return base
}

func nestedFieldBaseType(field protoreflect.FieldDescriptor) string {
	switch field.Kind() {
	case protoreflect.MessageKind:
		return string(field.Message().FullName())
	case protoreflect.EnumKind:
		return string(field.Enum().FullName())
	default:
		return scalarKindLabel(field.Kind())
	}
}

func scalarKindLabel(kind protoreflect.Kind) string {
	switch kind {
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return "bytes"
	case protoreflect.Int32Kind:
		return "int32"
	case protoreflect.Sint32Kind:
		return "sint32"
	case protoreflect.Sfixed32Kind:
		return "sfixed32"
	case protoreflect.Int64Kind:
		return "int64"
	case protoreflect.Sint64Kind:
		return "sint64"
	case protoreflect.Sfixed64Kind:
		return "sfixed64"
	case protoreflect.Uint32Kind:
		return "uint32"
	case protoreflect.Fixed32Kind:
		return "fixed32"
	case protoreflect.Uint64Kind:
		return "uint64"
	case protoreflect.Fixed64Kind:
		return "fixed64"
	case protoreflect.FloatKind:
		return "float"
	case protoreflect.DoubleKind:
		return "double"
	default:
		return strings.ToLower(kind.String())
	}
}

func describeEnumValues(enum protoreflect.EnumDescriptor) []enumValueDescription {
	values := make([]enumValueDescription, 0, enum.Values().Len())
	for i := 0; i < enum.Values().Len(); i++ {
		value := enum.Values().Get(i)
		values = append(values, enumValueDescription{
			Name:        string(value.Name()),
			Number:      int32(value.Number()),
			Description: cleanComment(commentText(value)),
		})
	}
	return values
}

type schemaCollector struct {
	messages map[protoreflect.FullName]protoreflect.MessageDescriptor
	enums    map[protoreflect.FullName]protoreflect.EnumDescriptor
}

func newSchemaCollector() *schemaCollector {
	return &schemaCollector{
		messages: map[protoreflect.FullName]protoreflect.MessageDescriptor{},
		enums:    map[protoreflect.FullName]protoreflect.EnumDescriptor{},
	}
}

func (c *schemaCollector) addMessage(message protoreflect.MessageDescriptor) {
	if message == nil || !strings.HasPrefix(string(message.FullName()), "rein.v1.") {
		return
	}
	if _, exists := c.messages[message.FullName()]; exists {
		return
	}
	c.messages[message.FullName()] = message
	for i := 0; i < message.Fields().Len(); i++ {
		field := message.Fields().Get(i)
		if field.IsMap() {
			if field.MapValue().Kind() == protoreflect.MessageKind {
				c.addMessage(field.MapValue().Message())
			}
			if field.MapValue().Kind() == protoreflect.EnumKind {
				c.addEnum(field.MapValue().Enum())
			}
			continue
		}
		if field.Kind() == protoreflect.MessageKind {
			c.addMessage(field.Message())
		}
		if field.Kind() == protoreflect.EnumKind {
			c.addEnum(field.Enum())
		}
	}
}

func (c *schemaCollector) addEnum(enum protoreflect.EnumDescriptor) {
	if enum == nil || !strings.HasPrefix(string(enum.FullName()), "rein.v1.") {
		return
	}
	if _, exists := c.enums[enum.FullName()]; exists {
		return
	}
	c.enums[enum.FullName()] = enum
}

func (c *schemaCollector) schemaIndex() schemaIndexDescription {
	messageNames := make([]string, 0, len(c.messages))
	for name := range c.messages {
		messageNames = append(messageNames, string(name))
	}
	sort.Strings(messageNames)
	messages := make([]namedSchemaDescription, 0, len(messageNames))
	for _, name := range messageNames {
		message := c.messages[protoreflect.FullName(name)]
		messages = append(messages, namedSchemaDescription{
			Name:        name,
			Description: cleanComment(commentText(message)),
		})
	}

	enumNames := make([]string, 0, len(c.enums))
	for name := range c.enums {
		enumNames = append(enumNames, string(name))
	}
	sort.Strings(enumNames)
	enums := make([]namedSchemaDescription, 0, len(enumNames))
	for _, name := range enumNames {
		enum := c.enums[protoreflect.FullName(name)]
		enums = append(enums, namedSchemaDescription{
			Name:        name,
			Description: cleanComment(commentText(enum)),
		})
	}

	return schemaIndexDescription{
		DetailFormat:  describeFormatMCPFull,
		DetailCommand: "rein [global flags] describe-as=mcp-full",
		MessageCount:  len(messages),
		EnumCount:     len(enums),
		Messages:      messages,
		Enums:         enums,
	}
}

func (c *schemaCollector) messageSchemas() []messageSchemaDescription {
	names := make([]string, 0, len(c.messages))
	for name := range c.messages {
		names = append(names, string(name))
	}
	sort.Strings(names)

	messages := make([]messageSchemaDescription, 0, len(names))
	for _, name := range names {
		message := c.messages[protoreflect.FullName(name)]
		fields := make([]fieldDescription, 0, message.Fields().Len())
		for i := 0; i < message.Fields().Len(); i++ {
			fields = append(fields, describeField(message.Fields().Get(i)))
		}
		messages = append(messages, messageSchemaDescription{
			Name:        name,
			Description: cleanComment(commentText(message)),
			Fields:      fields,
		})
	}
	return messages
}

func (c *schemaCollector) enumSchemas() []enumSchemaDescription {
	names := make([]string, 0, len(c.enums))
	for name := range c.enums {
		names = append(names, string(name))
	}
	sort.Strings(names)

	enums := make([]enumSchemaDescription, 0, len(names))
	for _, name := range names {
		enum := c.enums[protoreflect.FullName(name)]
		enums = append(enums, enumSchemaDescription{
			Name:        name,
			Description: cleanComment(commentText(enum)),
			Values:      describeEnumValues(enum),
		})
	}
	return enums
}

func renderDescribeAs(format string) (string, error) {
	switch format {
	case describeFormatCLI:
		surface, err := buildDescribeSurfaceFull()
		if err != nil {
			return "", err
		}
		return renderDescribeCLI(surface), nil
	case describeFormatMCP:
		surface, err := buildDescribeSurfaceCompact()
		if err != nil {
			return "", err
		}
		return renderDescribeMCP(surface), nil
	case describeFormatMCPFull:
		surface, err := buildDescribeSurfaceFull()
		if err != nil {
			return "", err
		}
		return renderDescribeMCPFull(surface), nil
	default:
		return "", fmt.Errorf("unsupported describe-as format %q (supported: %s)", format, supportedDescribeFormatNames())
	}
}

func supportedDescribeFormatNames() string {
	names := make([]string, 0, len(supportedDescribeFormats()))
	for _, format := range supportedDescribeFormats() {
		names = append(names, format.Name)
	}
	return strings.Join(names, ", ")
}

func renderDescribeCLI(surface describeSurface) string {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s SURFACE v1\n\n", strings.ToUpper(surface.Title))
	fmt.Fprintln(&buf, "NAME")
	fmt.Fprintf(&buf, "  %s\n", surface.Title)
	fmt.Fprintf(&buf, "    %s\n\n", surface.Summary)

	fmt.Fprintln(&buf, "USAGE")
	for _, line := range surface.Usage {
		fmt.Fprintf(&buf, "  %s\n", line)
	}
	buf.WriteString("\n")

	fmt.Fprintln(&buf, "CONVENTIONS")
	for _, line := range surface.Conventions {
		fmt.Fprintf(&buf, "  - %s\n", line)
	}
	buf.WriteString("\n")

	fmt.Fprintln(&buf, "GLOBAL FLAGS")
	for _, flag := range surface.GlobalFlags {
		fmt.Fprintf(&buf, "  --%s %s\n", flag.Name, flag.Type)
		fmt.Fprintf(&buf, "    %s\n", flag.Description)
	}
	buf.WriteString("\n")

	fmt.Fprintln(&buf, "DESCRIBE FORMATS")
	for _, format := range surface.Formats {
		fmt.Fprintf(&buf, "  %s\n", format.Name)
		fmt.Fprintf(&buf, "    %s\n", format.Description)
	}
	buf.WriteString("\n")

	fmt.Fprintln(&buf, "SERVICE GROUPS")
	for _, service := range surface.Services {
		fmt.Fprintf(&buf, "  %s\n", service.Name)
		fmt.Fprintf(&buf, "    summary: %s\n", service.Summary)
		fmt.Fprintf(&buf, "    cli: %s\n", service.CLI)
		fmt.Fprintf(&buf, "    help: %s\n", service.HelpCLI)
		fmt.Fprintln(&buf, "    verbs:")
		for _, verb := range service.Verbs {
			fmt.Fprintf(&buf, "      %s\n", verb.Name)
			fmt.Fprintf(&buf, "        cli: %s\n", verb.CLI)
			fmt.Fprintf(&buf, "        summary: %s\n", verb.Summary)
		}
		buf.WriteString("\n")
	}

	fmt.Fprintln(&buf, "UTILITY COMMANDS")
	for _, utility := range surface.UtilityCommands {
		fmt.Fprintf(&buf, "  %s\n", utility.Command)
		fmt.Fprintf(&buf, "    summary: %s\n", utility.Summary)
		fmt.Fprintf(&buf, "    cli: %s\n", utility.CLI)
		if len(utility.Arguments) == 0 {
			fmt.Fprintln(&buf, "    args: none")
		} else {
			fmt.Fprintln(&buf, "    args:")
			for _, arg := range utility.Arguments {
				fmt.Fprintf(&buf, "      <%s> %s\n", arg.Name, arg.Type)
				fmt.Fprintf(&buf, "        required: %t\n", arg.Required)
				fmt.Fprintf(&buf, "        description: %s\n", arg.Description)
			}
		}
		if len(utility.Flags) == 0 {
			fmt.Fprintln(&buf, "    flags: none")
		} else {
			fmt.Fprintln(&buf, "    flags:")
			for _, flag := range utility.Flags {
				fmt.Fprintf(&buf, "      --%s %s\n", flag.Name, flag.Type)
				fmt.Fprintf(&buf, "        %s\n", flag.Description)
			}
		}
		buf.WriteString("\n")
	}

	fmt.Fprintln(&buf, "RPC COMMANDS")
	for _, command := range surface.Commands {
		fmt.Fprintf(&buf, "  COMMAND %s %s\n", command.Noun, command.Verb)
		fmt.Fprintf(&buf, "    summary: %s\n", command.Summary)
		fmt.Fprintf(&buf, "    grpc_service: %s\n", command.Service)
		fmt.Fprintf(&buf, "    grpc_method: %s\n", command.Method)
		fmt.Fprintf(&buf, "    full_method: %s\n", command.FullMethod)
		fmt.Fprintf(&buf, "    request: %s\n", command.RequestType)
		fmt.Fprintf(&buf, "    response: %s\n", command.ResponseType)
		fmt.Fprintf(&buf, "    cli: %s\n", command.CLI)
		fmt.Fprintf(&buf, "    flags:\n")
		for _, flag := range command.Flags {
			fmt.Fprintf(&buf, "      --%s %s\n", flag.Name, flag.Type)
			fmt.Fprintf(&buf, "        proto: %s\n", flag.ProtoType)
			fmt.Fprintf(&buf, "        description: %s\n", flag.Description)
			fmt.Fprintf(&buf, "        accepts_json: %t\n", flag.AcceptsJSON)
			if flag.SchemaRef != "" {
				fmt.Fprintf(&buf, "        schema: %s\n", flag.SchemaRef)
			}
			if len(flag.EnumValues) > 0 {
				fmt.Fprintf(&buf, "        enum_values:\n")
				for _, value := range flag.EnumValues {
					fmt.Fprintf(&buf, "          - %s = %d\n", value.Name, value.Number)
				}
			}
		}
		buf.WriteString("\n")
	}

	fmt.Fprintln(&buf, "GATEWAY STUB")
	fmt.Fprintln(&buf, "  routes:")
	for _, route := range surface.Gateway.Routes {
		fmt.Fprintf(&buf, "    %s %s\n", route.Method, route.Path)
		fmt.Fprintf(&buf, "      purpose: %s\n", route.Purpose)
	}
	fmt.Fprintln(&buf, "  streams:")
	for _, stream := range surface.Gateway.Streams {
		fmt.Fprintf(&buf, "    %s\n", stream.Path)
		fmt.Fprintf(&buf, "      event: %s\n", stream.Event)
		fmt.Fprintf(&buf, "      purpose: %s\n", stream.Purpose)
	}
	buf.WriteString("\n")

	fmt.Fprintln(&buf, "SCHEMA INDEX")
	fmt.Fprintf(&buf, "  detail_format: %s\n", surface.SchemaIndex.DetailFormat)
	fmt.Fprintf(&buf, "  detail_command: %s\n", surface.SchemaIndex.DetailCommand)
	fmt.Fprintf(&buf, "  message_count: %d\n", surface.SchemaIndex.MessageCount)
	fmt.Fprintf(&buf, "  enum_count: %d\n", surface.SchemaIndex.EnumCount)
	buf.WriteString("\n")

	fmt.Fprintln(&buf, "SCHEMAS")
	for _, message := range surface.Messages {
		fmt.Fprintf(&buf, "  MESSAGE %s\n", message.Name)
		if message.Description != "" {
			fmt.Fprintf(&buf, "    summary: %s\n", message.Description)
		}
		fmt.Fprintln(&buf, "    fields:")
		for _, field := range message.Fields {
			fmt.Fprintf(&buf, "      %s %s\n", field.Name, field.Type)
			fmt.Fprintf(&buf, "        proto: %s\n", field.ProtoType)
			fmt.Fprintf(&buf, "        description: %s\n", field.Description)
			fmt.Fprintf(&buf, "        accepts_json: %t\n", field.AcceptsJSON)
			if field.SchemaRef != "" {
				fmt.Fprintf(&buf, "        schema: %s\n", field.SchemaRef)
			}
			if len(field.EnumValues) > 0 {
				fmt.Fprintln(&buf, "        enum_values:")
				for _, value := range field.EnumValues {
					fmt.Fprintf(&buf, "          - %s = %d\n", value.Name, value.Number)
				}
			}
		}
		buf.WriteString("\n")
	}
	for _, enum := range surface.Enums {
		fmt.Fprintf(&buf, "  ENUM %s\n", enum.Name)
		if enum.Description != "" {
			fmt.Fprintf(&buf, "    summary: %s\n", enum.Description)
		}
		for _, value := range enum.Values {
			fmt.Fprintf(&buf, "    %s = %d\n", value.Name, value.Number)
			if value.Description != "" {
				fmt.Fprintf(&buf, "      %s\n", value.Description)
			}
		}
		buf.WriteString("\n")
	}
	return buf.String()
}

func renderDescribeMCP(surface describeSurface) string {
	return renderDescribeMCPDocument(surface, false)
}

func renderDescribeMCPFull(surface describeSurface) string {
	return renderDescribeMCPDocument(surface, true)
}

func renderDescribeMCPDocument(surface describeSurface, includeDetailedSchemas bool) string {
	var buf bytes.Buffer
	writeYAMLString(&buf, "version", "1")
	writeYAMLString(&buf, "surface", surface.Title)
	writeYAMLString(&buf, "description", surface.Summary)
	writeYAMLString(&buf, "describe_command", "rein [global flags] describe-as=<format>")
	buf.WriteString("conventions:\n")
	for _, convention := range surface.Conventions {
		fmt.Fprintf(&buf, "  - %s\n", yamlString(convention))
	}
	buf.WriteString("formats:\n")
	for _, format := range surface.Formats {
		buf.WriteString("  - name: ")
		buf.WriteString(yamlString(format.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "    description: %s\n", yamlString(format.Description))
	}
	buf.WriteString("root_usage:\n")
	for _, line := range surface.Usage {
		fmt.Fprintf(&buf, "  - %s\n", yamlString(line))
	}
	buf.WriteString("global_flags:\n")
	for _, flag := range surface.GlobalFlags {
		buf.WriteString("  - name: ")
		buf.WriteString(yamlString(flag.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "    type: %s\n", yamlString(flag.Type))
		fmt.Fprintf(&buf, "    description: %s\n", yamlString(flag.Description))
	}
	buf.WriteString("services:\n")
	for _, service := range surface.Services {
		buf.WriteString("  - name: ")
		buf.WriteString(yamlString(service.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "    cli: %s\n", yamlString(service.CLI))
		fmt.Fprintf(&buf, "    help_cli: %s\n", yamlString(service.HelpCLI))
		fmt.Fprintf(&buf, "    summary: %s\n", yamlString(service.Summary))
		buf.WriteString("    verbs:\n")
		for _, verb := range service.Verbs {
			buf.WriteString("      - name: ")
			buf.WriteString(yamlString(verb.Name))
			buf.WriteString("\n")
			fmt.Fprintf(&buf, "        cli: %s\n", yamlString(verb.CLI))
			fmt.Fprintf(&buf, "        summary: %s\n", yamlString(verb.Summary))
		}
	}
	buf.WriteString("utility_commands:\n")
	for _, utility := range surface.UtilityCommands {
		buf.WriteString("  - name: ")
		buf.WriteString(yamlString(utility.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "    command: %s\n", yamlString(utility.Command))
		fmt.Fprintf(&buf, "    cli: %s\n", yamlString(utility.CLI))
		fmt.Fprintf(&buf, "    summary: %s\n", yamlString(utility.Summary))
		buf.WriteString("    arguments:\n")
		for _, arg := range utility.Arguments {
			buf.WriteString("      - name: ")
			buf.WriteString(yamlString(arg.Name))
			buf.WriteString("\n")
			fmt.Fprintf(&buf, "        type: %s\n", yamlString(arg.Type))
			fmt.Fprintf(&buf, "        description: %s\n", yamlString(arg.Description))
			fmt.Fprintf(&buf, "        required: %t\n", arg.Required)
		}
		buf.WriteString("    flags:\n")
		for _, flag := range utility.Flags {
			buf.WriteString("      - name: ")
			buf.WriteString(yamlString(flag.Name))
			buf.WriteString("\n")
			fmt.Fprintf(&buf, "        type: %s\n", yamlString(flag.Type))
			fmt.Fprintf(&buf, "        description: %s\n", yamlString(flag.Description))
		}
	}
	buf.WriteString("commands:\n")
	for _, command := range surface.Commands {
		buf.WriteString("  - name: ")
		buf.WriteString(yamlString(command.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "    cli: %s\n", yamlString(command.CLI))
		fmt.Fprintf(&buf, "    noun: %s\n", yamlString(command.Noun))
		fmt.Fprintf(&buf, "    verb: %s\n", yamlString(command.Verb))
		fmt.Fprintf(&buf, "    service: %s\n", yamlString(command.Service))
		fmt.Fprintf(&buf, "    service_summary: %s\n", yamlString(command.ServiceSummary))
		fmt.Fprintf(&buf, "    grpc_method: %s\n", yamlString(command.Method))
		fmt.Fprintf(&buf, "    full_method: %s\n", yamlString(command.FullMethod))
		fmt.Fprintf(&buf, "    summary: %s\n", yamlString(command.Summary))
		fmt.Fprintf(&buf, "    request_type: %s\n", yamlString(command.RequestType))
		fmt.Fprintf(&buf, "    response_type: %s\n", yamlString(command.ResponseType))
		buf.WriteString("    flags:\n")
		for _, flag := range command.Flags {
			writeYAMLField(&buf, "      ", flag)
		}
	}
	buf.WriteString("gateway:\n")
	buf.WriteString("  routes:\n")
	for _, route := range surface.Gateway.Routes {
		buf.WriteString("    - method: ")
		buf.WriteString(yamlString(route.Method))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "      path: %s\n", yamlString(route.Path))
		fmt.Fprintf(&buf, "      purpose: %s\n", yamlString(route.Purpose))
	}
	buf.WriteString("  streams:\n")
	for _, stream := range surface.Gateway.Streams {
		buf.WriteString("    - path: ")
		buf.WriteString(yamlString(stream.Path))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "      event: %s\n", yamlString(stream.Event))
		fmt.Fprintf(&buf, "      purpose: %s\n", yamlString(stream.Purpose))
	}
	buf.WriteString("schema_index:\n")
	fmt.Fprintf(&buf, "  detail_format: %s\n", yamlString(surface.SchemaIndex.DetailFormat))
	fmt.Fprintf(&buf, "  detail_command: %s\n", yamlString(surface.SchemaIndex.DetailCommand))
	fmt.Fprintf(&buf, "  message_count: %d\n", surface.SchemaIndex.MessageCount)
	fmt.Fprintf(&buf, "  enum_count: %d\n", surface.SchemaIndex.EnumCount)
	buf.WriteString("  messages:\n")
	for _, message := range surface.SchemaIndex.Messages {
		buf.WriteString("    - name: ")
		buf.WriteString(yamlString(message.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "      description: %s\n", yamlString(message.Description))
	}
	buf.WriteString("  enums:\n")
	for _, enum := range surface.SchemaIndex.Enums {
		buf.WriteString("    - name: ")
		buf.WriteString(yamlString(enum.Name))
		buf.WriteString("\n")
		fmt.Fprintf(&buf, "      description: %s\n", yamlString(enum.Description))
	}
	if includeDetailedSchemas {
		buf.WriteString("schemas:\n")
		buf.WriteString("  messages:\n")
		for _, message := range surface.Messages {
			buf.WriteString("    - name: ")
			buf.WriteString(yamlString(message.Name))
			buf.WriteString("\n")
			fmt.Fprintf(&buf, "      description: %s\n", yamlString(message.Description))
			buf.WriteString("      fields:\n")
			for _, field := range message.Fields {
				writeYAMLField(&buf, "        ", field)
			}
		}
		buf.WriteString("  enums:\n")
		for _, enum := range surface.Enums {
			buf.WriteString("    - name: ")
			buf.WriteString(yamlString(enum.Name))
			buf.WriteString("\n")
			fmt.Fprintf(&buf, "      description: %s\n", yamlString(enum.Description))
			buf.WriteString("      values:\n")
			for _, value := range enum.Values {
				buf.WriteString("        - name: ")
				buf.WriteString(yamlString(value.Name))
				buf.WriteString("\n")
				fmt.Fprintf(&buf, "          number: %d\n", value.Number)
				fmt.Fprintf(&buf, "          description: %s\n", yamlString(value.Description))
			}
		}
	}
	return buf.String()
}

func writeYAMLField(buf *bytes.Buffer, indent string, field fieldDescription) {
	fmt.Fprintf(buf, "%s- name: %s\n", indent, yamlString(field.Name))
	fmt.Fprintf(buf, "%s  type: %s\n", indent, yamlString(field.Type))
	fmt.Fprintf(buf, "%s  proto_type: %s\n", indent, yamlString(field.ProtoType))
	fmt.Fprintf(buf, "%s  description: %s\n", indent, yamlString(field.Description))
	fmt.Fprintf(buf, "%s  accepts_json: %t\n", indent, field.AcceptsJSON)
	fmt.Fprintf(buf, "%s  repeated: %t\n", indent, field.Repeated)
	fmt.Fprintf(buf, "%s  map: %t\n", indent, field.Map)
	fmt.Fprintf(buf, "%s  schema_ref: %s\n", indent, yamlString(field.SchemaRef))
	if len(field.EnumValues) == 0 {
		fmt.Fprintf(buf, "%s  enum_values: []\n", indent)
		return
	}
	buf.WriteString(indent)
	buf.WriteString("  enum_values:\n")
	for _, value := range field.EnumValues {
		fmt.Fprintf(buf, "%s    - name: %s\n", indent, yamlString(value.Name))
		fmt.Fprintf(buf, "%s      number: %d\n", indent, value.Number)
		fmt.Fprintf(buf, "%s      description: %s\n", indent, yamlString(value.Description))
	}
}

func yamlString(value string) string {
	return strconv.Quote(value)
}

func writeYAMLString(buf *bytes.Buffer, key, value string) {
	fmt.Fprintf(buf, "%s: %s\n", key, yamlString(value))
}
