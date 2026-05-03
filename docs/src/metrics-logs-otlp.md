# Metrics, logs, and OTLP

Rein's observability story is intentionally simple in this phase:

- daemon logs always go to stdout as structured text
- OTLP export is opt-in
- when OTLP is enabled, rein exports traces, metrics, and logs over OTLP/gRPC
- the repo ships a bundled SigNoz dashboard pack you can apply with `rein dashboards apply`

## OTLP is off by default

If you do not set an OTLP endpoint, rein keeps its current no-op exporter behavior.

This means:

- no OTLP traces
- no OTLP metrics
- no OTLP log export
- stdout logging still works

## Enable OTLP with environment variables

The daemon honors these environment variables:

- `OTEL_EXPORTER_OTLP_ENDPOINT`
- `OTEL_EXPORTER_OTLP_HEADERS`
- `OTEL_EXPORTER_OTLP_INSECURE`
- `OTEL_SERVICE_NAME`
- `OTEL_RESOURCE_ATTRIBUTES`

Example:

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4317 \
OTEL_EXPORTER_OTLP_INSECURE=true \
OTEL_EXPORTER_OTLP_HEADERS='authorization=Bearer replace-me' \
OTEL_SERVICE_NAME=rein-daemon \
OTEL_RESOURCE_ATTRIBUTES='deployment.environment=dev,service.namespace=rein' \
./bin/rein daemon serve
```

## Enable OTLP with CLI flags

You can override the daemon's OTLP settings directly on the command line:

```bash
./bin/rein daemon serve \
  --otlp-endpoint localhost:4317 \
  --otlp-insecure \
  --otlp-headers 'authorization=Bearer replace-me'
```

When present, the CLI flags override the corresponding environment-derived values.

## Endpoint format notes

Rein accepts:

- bare `host:port` endpoints
- `http://host:port` URLs
- `https://host:port` URLs

Normalization rules:

- `http://...` is normalized to `host:port` and implies insecure transport
- `https://...` is normalized to `host:port` and keeps TLS enabled
- bare `host:port` stays as-is, so add `--otlp-insecure` or `OTEL_EXPORTER_OTLP_INSECURE=true` when your collector expects plaintext gRPC

## What gets exported

### Logs

The daemon always writes structured text logs to stdout.

When OTLP is enabled, those same slog events are also sent through the OTLP log exporter. In the current tree, that primarily means daemon lifecycle/startup logs and any future daemon-side slog events.

### Metrics

The current custom metric set is intentionally bounded:

- `rein.daemon.starts`
- `rein.daemon.running`
- `rein.rpc.requests`
- `rein.rpc.errors`
- `rein.rpc.duration`

The RPC instruments are annotated with gRPC attributes such as:

- `rpc.system=grpc`
- `rpc.service`
- `rpc.method`
- `rpc.stream`
- `rpc.grpc.status_code`
- `rpc.grpc.status_text`

### Traces

Each unary or streaming RPC handled by the daemon becomes a server span with the same gRPC attribute set.

## Resource attributes

Rein always sets instance-aware resource information for the daemon runtime:

- `service.name` defaults to `rein-daemon`
- `rein.instance` is set to the selected instance name
- `service.instance.id` is set to the selected instance name

`OTEL_SERVICE_NAME` overrides `service.name`.

`OTEL_RESOURCE_ATTRIBUTES` is merged in before the daemon adds the instance-specific attributes, so the selected instance still appears consistently.

## Diagnostics and dashboards

Use `rein doctor` for local readiness checks:

```bash
./bin/rein doctor
```

Use the bundled dashboard installer to push the reference SigNoz dashboards:

```bash
SIGNOZ_BASE_URL=http://localhost:3301 \
SIGNOZ_API_KEY=replace-me \
./bin/rein dashboards apply
```

The dashboards marketplace currently points at the bundled local `rein-dashboards` plugin under `plugins/rein-dashboards/`. Repeat runs skip unchanged bundled dashboards and report `createdIds`, `updatedIds`, and `skippedIds` in the JSON response.

## Current limits

- OTLP export is daemon-only; the CLI itself does not expose a separate observability pipeline
- `rein dashboards apply` supports bundled local dashboard plugins today, not remote bootstrap
- the TUI can show looking-glass/tail support state, but rein does not yet stream live tails

## Related docs

- [Overview](README.md)
- [CLI quickstart](cli-quickstart.md)
- [TUI quickstart](tui-quickstart.md)
- [Plugin author guide](plugin-author-guide.md)
- [Migration guide](migration-guide.md)
- [Docs summary](SUMMARY.md)

<div hidden>

[[README|Overview]]
[[cli-quickstart|CLI quickstart]]
[[tui-quickstart|TUI quickstart]]
[[plugin-author-guide|Plugin author guide]]
[[migration-guide|Migration guide]]
[[SUMMARY|Docs summary]]

</div>
