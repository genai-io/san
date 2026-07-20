package task

import "bytes"

// maxOutputBufferSize caps what a task keeps in memory. The full output is
// always on disk in OutputFile, so this buffer only has to serve TaskOutput's
// inline preview — and nothing prunes tasks (see Manager), so without a cap it
// would be held for as long as the process runs.
const maxOutputBufferSize = 512 * 1024

// appendCapped appends data to buf, keeping only the trailing
// maxOutputBufferSize bytes. Shared by both task types so neither can drift
// from the cap: BashTask used to skip the one AgentTask applied.
//
// Both the clamp and the trim happen before the write, so peak memory stays
// proportional to the cap rather than to the data. That is what matters here:
// the bash tool hands a whole run's output over in a single call, so writing
// first would grow the buffer to the full output size just to throw all but
// the last 512KB away.
func appendCapped(buf *bytes.Buffer, data []byte) {
	if len(data) > maxOutputBufferSize {
		data = data[len(data)-maxOutputBufferSize:]
	}

	// Make room by shifting the survivors to the front. copy is memmove, so
	// the overlapping source and destination are safe and no second buffer is
	// needed. Clamping data above keeps over within the buffer's length.
	if over := buf.Len() + len(data) - maxOutputBufferSize; over > 0 {
		kept := buf.Bytes()
		buf.Truncate(copy(kept, kept[over:]))
	}
	buf.Write(data)
}
