package build

// Test-only re-exports of internal helpers. The Build entry point couples
// directly to the Moby SDK (provider.Client returns *client.Client) so
// driving a full ImageBuild/ImagePull without docker is impractical without
// rewriting production code. Instead we cover the pure helpers — tar
// streaming, image-ref formatting, sha shortening, the JSON message
// streamer — plus the validation paths in Build that reject early.

import (
	"io"
)

var (
	TarDirectory      = tarDirectory
	ImageRefFor       = imageRefFor
	ShortenSHA        = shortenSHA
	SplitLines        = splitLines
	ReadGitHead       = readGitHead
	StreamJSONMessage = func(e *Engine, rc io.Reader, sink LogSink, opts BuildOptions) error {
		return e.streamJSONMessage(rc, sink, opts)
	}
)
