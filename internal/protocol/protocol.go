// Package protocol defines the HTTP API the broker serves to the gateway over
// its unix socket. Both sides import it, so a typo becomes a compile error.
//
// Request:
//
//	GET ItemsPath?URIParam=pass://SHARE/ITEM[/FIELD]
//
// Responses:
//
//	200  raw pass-cli JSON, Content-Type: application/json
//	400  missing or invalid uri
//	405  method other than GET
//	502  pass-cli failed
//	503  too many pass-cli calls already running
//	504  pass-cli timed out
//
// Error bodies are fixed messages: they never carry pass-cli output or error
// details, which stay in the broker logs.
package protocol

const (
	// ItemsPath is the broker endpoint returning one Proton Pass item.
	ItemsPath = "/v1/items"
	// URIParam is the query parameter holding the pass:// URI to read.
	URIParam = "uri"
)
