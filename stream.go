package main

// stream.go — bounds how long a slow client can hold a response.

import (
	"net/http"
	"time"
)

// writeStall is how long one write to the client may block. Streaming code
// renews it before every event, so a reader that stops reading loses the
// connection; that cancels the request context and with it the upstream call
// and its concurrency slot.
var writeStall = 60 * time.Second

// streamFlush renews the write deadline for the next write and flushes what
// was written. Call it after every event of a stream.
func streamFlush(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	rc.SetWriteDeadline(time.Now().Add(writeStall))
	rc.Flush()
}

// renewWriteDeadline sets the deadline before the first write of a stream.
func renewWriteDeadline(w http.ResponseWriter) {
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(writeStall))
}
