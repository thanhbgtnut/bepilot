package agent

import (
	"io"
	"log/slog"

	"github.com/cloudwego/eino/callbacks"
)

// testCallbackHandler is newCallbackHandler with a discard logger, for tests
// that only care about the assembler side effects.
func testCallbackHandler(asm *assembler) callbacks.Handler {
	return newCallbackHandler(asm, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-model")
}
