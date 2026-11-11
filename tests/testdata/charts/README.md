# Chart renders

Real `helm template` output from public charts, rendered with Helm v3.16.3 on 2026-11-11. The charts are Apache-2.0 licensed. The values files used are in `values/`. Where a file name says `bad`, `divisor` or `gogc-off`, the chart is real and the values add a mistake on purpose, through the chart's own `env` or `extraEnvs` setting.

| File | Chart | Version | Command |
|---|---|---|---|
| `traefik-healthy.yaml` | traefik/traefik | 42.3.2 | `helm template edge traefik/traefik -n edge -f values/traefik-healthy.yaml` |
| `traefik-bad.yaml` | traefik/traefik | 42.3.2 | same, with `values/traefik-bad.yaml` |
| `traefik-cpu-divisor.yaml` | traefik/traefik | 42.3.2 | same, with `values/traefik-cpu-divisor.yaml` |
| `otel-healthy.yaml` | open-telemetry/opentelemetry-collector | 0.178.3 | `helm template col open-telemetry/opentelemetry-collector -n obs -f values/otel-healthy.yaml` |
| `otel-gogc-off.yaml` | open-telemetry/opentelemetry-collector | 0.178.3 | same, with `values/otel-gogc-off.yaml` |
| `otel-divisor.yaml` | open-telemetry/opentelemetry-collector | 0.178.3 | same, with `values/otel-divisor.yaml` |
| `certmanager-default.yaml` | jetstack/cert-manager | v1.21.3 | `helm template cm jetstack/cert-manager -n cert-manager` |
| `certmanager-limits.yaml` | jetstack/cert-manager | v1.21.3 | same, with `values/certmanager-limits.yaml` |
