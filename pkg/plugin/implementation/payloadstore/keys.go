package payloadstore

// msgKey returns the cache key for a single payload entry, namespaced by module name
// to prevent collisions between handlers sharing the same cache backend.
func msgKey(namespace, messageID string) string {
	return "payload:" + namespace + ":msg:" + messageID
}

// txnIndexKey returns the cache key for the transaction message-ID index.
func txnIndexKey(namespace, transactionID string) string {
	return "payload:" + namespace + ":txn:" + transactionID + ":index"
}
