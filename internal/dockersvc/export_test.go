package dockersvc

// Test-only re-exports of unexported parsing helpers. Production code does not
// import these — they live behind the public RunContainer surface — but unit
// tests get more useful coverage exercising them directly than driving
// container creation against a fake docker client.

var (
	ParseMemory        = parseMemory
	ParseMemorySwap    = parseMemorySwap
	ParseCPUs          = parseCPUs
	ParseResources     = parseResources
	VersionAtLeast     = versionAtLeast
	ParseDottedVersion = parseDottedVersion
)
