package agent

import (
	"errors"
	"io"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// Local aliases to keep signatures readable.
type (
	reactAgent      = *react.Agent
	einoMessage     = schema.Message
	callbackHandler = callbacks.Handler
)

func isEOF(err error) bool { return errors.Is(err, io.EOF) }

// summarize / maybeSummarize live in summary.go
