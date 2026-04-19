# Logging

fmlocal uses Go's `log/slog` library and writes structured text logs to stderr. The log level is configured per environment in the YAML config file.

## Log levels

| Level   | What is logged |
|---------|----------------|
| `debug` | API requests (action, HTTP status, duration), publisher calls (event name, ticket ID) |
| `info`  | Server startup messages |
| `warn`  | Publisher failures (event dropped, delivery error) |
| `error` | Fatal handler errors |

`debug` is verbose — every incoming API call and every event publish attempt is logged. Use it during development to trace request flow and event delivery. Use `info` or higher in environments where log volume matters.

## Setting the log level

Set `server.logLevel` in `config.yaml` (`debug` | `info` | `warn` | `error`, default `info`). See [Configuration reference — server](config.md#server) for the full field description. An unknown value causes fmlocal to exit with a config error at startup.

## Local Docker Compose setup

`deploy/local/config.yaml` sets `logLevel: debug` by default. Logs stream to stderr and are visible via:

```sh
docker compose logs -f fmlocal
```

## Native `go run`

Set the level in whichever config file you pass to `-config`:

```sh
go run ./cmd/fmlocal -config local.yaml
```

`testdata/config/sample.yaml` uses `logLevel: info`.
