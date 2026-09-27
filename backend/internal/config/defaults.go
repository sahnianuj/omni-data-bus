package config

// Development defaults. They match deploy/compose/dev.yml so `make dev` works with no
// environment variables; production must override them (see README).
const (
	DefaultHTTPAddr     = ":8080"
	DefaultPublicURL    = "http://localhost:5173"
	DefaultMetaConnStr  = "couchbase://localhost"
	DefaultMetaUsername = "Administrator"
	DefaultMetaPassword = "password"
	DefaultMetaBucket   = "omniconnect"
	DefaultMetaScope    = "meta"
	// DevKVPort and DevMgmtPort are the Couchbase ports the SDK needs on localhost.
	DevKVPort   = "11210"
	DevMgmtPort = "8091"
)
