package task

import "bytes"

// maxOutputBufferSize caps what a task keeps in memory. The full output is
// always on disk in OutputFile, so the buffer only has to serve TaskOutput's
// inline preview — and the manager never forgets a task (see Manager), so
// without a cap a chatty background command's every byte would be held for the
// life of the process.
const maxOutputBufferSize = 512 * 1024

// appendCapped appends data to buf, keeping only the trailing
// maxOutputBufferSize bytes. Shared by both task types so neither can drift
// from the cap: BashTask used to skip the one AgentTask applied.
//
// The trim happens before the write, not after, so peak memory stays
// proportional to the cap rather than to the data. That is the difference that
// matters here: the bash tool hands a whole run's output over in a single
// call, so writing first would grow the buffer to the full output size just to
// throw all but the last 512KB away.
func appendCapped(buf *bytes.Buffer, data []byte) {
	if len(data) >= maxOutputBufferSize {
		buf.Reset()
		buf.Write(data[len(data)-maxOutputBufferSize:])
		return
	}

	// Drop the oldest bytes by shifting the survivors to the front. copy is
	// memmove, so the overlapping source and destination are safe and no
	// second buffer is needed.
	if over := buf.Len() + len(data) - maxOutputBufferSize; over > 0 {
		kept := buf.Bytes()
		buf.Truncate(copy(kept, kept[over:]))
	}
	buf.Write(data)
}
