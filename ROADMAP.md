# Roadmap

## Next
- Pod-level `spec.resources` (Kubernetes 1.32 and newer) as the limit when a container has none
- `GODEBUG` values that the Go version in the image doesn't know, given the Go version with a flag
- Read the Go version from a binary or image tag to decide whether GOMAXPROCS follows the CPU limit by itself (Go 1.25 and newer)

## Later
- A GitHub Action that renders a chart and comments the findings on a pull request
- Argo Rollouts and Knative Services by name, beyond the generic `spec.template` match
- Values from Secrets, when the user passes a decrypted file on purpose
