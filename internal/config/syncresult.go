package config

import "os"

// SyncResult, when HL_SYNC_RESULT=true, makes POST /action run the processor
// inline and return the final ActionResult in the HTTP response, instead of
// returning "pending" and delivering the result via the node->proxy path. This
// enables proxy-less Direct-TEE: the relayer reads the result straight from
// /action, so no ext-proxy, signing policy, or indexer is required. Default
// false preserves the original asynchronous behaviour.
var SyncResult = os.Getenv("HL_SYNC_RESULT") == "true"
