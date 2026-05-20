// Package docker is a thin wrapper around the Moby Docker Engine SDK
// (github.com/docker/docker/client). It provides:
//
//   - A Provider interface representing a Docker engine reachable either via
//     the local UNIX socket or over SSH.
//   - Helpers to ensure the shared "prexel-net" bridge network exists.
//   - Helpers to run, stop, remove, rename, list containers, with sensible
//     defaults (always attached to prexel-net, with the container name as a
//     network alias).
//   - A log streamer suited for SSE.
//
// All operations take a context.Context.
package dockersvc

import "errors"

// Sentinel errors. Consumers should use errors.Is to match.
var (
	// ErrContainerNotFound is returned when a container lookup by name/id misses.
	ErrContainerNotFound = errors.New("docker: container not found")

	// ErrImageNotFound is returned when an image reference cannot be located.
	ErrImageNotFound = errors.New("docker: image not found")

	// ErrNetworkNotFound is returned when a network lookup misses.
	ErrNetworkNotFound = errors.New("docker: network not found")

	// ErrUnsupportedDockerVersion is returned by Version() when the remote
	// engine reports an API/server version below the project minimum (20.10).
	ErrUnsupportedDockerVersion = errors.New("docker: engine version is below the supported minimum (20.10)")

	// ErrInvalidResource is returned when a resource string (memory/cpu limits)
	// cannot be parsed.
	ErrInvalidResource = errors.New("docker: invalid resource specification")
)
