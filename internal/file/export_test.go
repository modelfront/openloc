package file

// MaterializeStoredFile exposes publish's per-file step to the integration
// tests, which hand it a file row read before a concurrent change.
var MaterializeStoredFile = (*Handler).materializeStoredFile
