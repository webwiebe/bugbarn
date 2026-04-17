package bugbarn

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

const (
	sdkName    = "bugbarn.go"
	sdkVersion = "0.1.0"
)

type stackFrame struct {
	Function string `json:"function,omitempty"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Module   string `json:"module,omitempty"`
}

type userContext struct {
	ID       string `json:"id,omitempty"`
	Email    string `json:"email,omitempty"`
	Username string `json:"username,omitempty"`
}

type envelope struct {
	Timestamp    string         `json:"timestamp"`
	SeverityText string         `json:"severityText"`
	Body         string         `json:"body"`
	Exception    exceptionBlock `json:"exception"`
	Attributes   map[string]any `json:"attributes,omitempty"`
	User         *userContext   `json:"user,omitempty"`
	Sender       senderBlock    `json:"sender"`
}

type exceptionBlock struct {
	Type       string       `json:"type"`
	Message    string       `json:"message"`
	Stacktrace []stackFrame `json:"stacktrace,omitempty"`
}

type senderBlock struct {
	SDK sdkBlock `json:"sdk"`
}

type sdkBlock struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func buildEnvelope(err error, opts captureOpts) envelope {
	msg := ""
	typ := "Error"
	if err != nil {
		msg = err.Error()
		typ = fmt.Sprintf("%T", err)
		// strip package prefix for cleaner type names
		if idx := strings.LastIndex(typ, "."); idx >= 0 {
			typ = typ[idx+1:]
		}
	}

	attrs := make(map[string]any)
	for k, v := range opts.attributes {
		attrs[k] = v
	}
	if opts.release != "" {
		attrs["release"] = opts.release
	}
	if opts.environment != "" {
		attrs["environment"] = opts.environment
	}

	env := envelope{
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
		SeverityText: "ERROR",
		Body:         msg,
		Exception:    exceptionBlock{Type: typ, Message: msg, Stacktrace: captureStacktrace(3)},
		Sender:       senderBlock{SDK: sdkBlock{Name: sdkName, Version: sdkVersion}},
	}
	if len(attrs) > 0 {
		env.Attributes = attrs
	}
	if opts.user != nil {
		env.User = opts.user
	}
	return env
}

func buildMessageEnvelope(msg string, opts captureOpts) envelope {
	fakeErr := fmt.Errorf("%s", msg)
	env := buildEnvelope(fakeErr, opts)
	env.Exception.Type = "Error"
	env.Exception.Message = msg
	env.Exception.Stacktrace = captureStacktrace(3)
	return env
}

func buildPanicEnvelope(recovered any, stack []byte) envelope {
	msg := fmt.Sprint(recovered)
	frames := parsePanicStack(stack)
	return envelope{
		Timestamp:    time.Now().UTC().Format(time.RFC3339Nano),
		SeverityText: "FATAL",
		Body:         msg,
		Exception:    exceptionBlock{Type: "panic", Message: msg, Stacktrace: frames},
		Sender:       senderBlock{SDK: sdkBlock{Name: sdkName, Version: sdkVersion}},
	}
}

func captureStacktrace(skip int) []stackFrame {
	pcs := make([]uintptr, 32)
	n := runtime.Callers(skip+1, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var out []stackFrame
	for {
		f, more := frames.Next()
		if strings.Contains(f.File, "runtime/") {
			if !more {
				break
			}
			continue
		}
		module := f.File
		if idx := strings.LastIndex(module, "/"); idx >= 0 {
			module = module[idx+1:]
		}
		out = append(out, stackFrame{
			Function: f.Function,
			File:     f.File,
			Line:     f.Line,
			Module:   module,
		})
		if !more {
			break
		}
	}
	return out
}

func parsePanicStack(stack []byte) []stackFrame {
	// Parse goroutine stack from debug.Stack() output.
	lines := strings.Split(string(stack), "\n")
	var frames []stackFrame
	for i := 0; i+1 < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		next := strings.TrimSpace(lines[i+1])
		if strings.HasSuffix(line, ")") && strings.HasPrefix(next, "/") {
			fn := line
			if idx := strings.LastIndex(fn, "("); idx >= 0 {
				fn = fn[:idx]
			}
			file := next
			lineNum := 0
			if idx := strings.LastIndex(file, ":"); idx >= 0 {
				fmt.Sscanf(file[idx+1:], "%d", &lineNum)
				file = file[:idx]
			}
			module := file
			if idx := strings.LastIndex(module, "/"); idx >= 0 {
				module = module[idx+1:]
			}
			frames = append(frames, stackFrame{Function: fn, File: file, Line: lineNum, Module: module})
			i++ // skip the file line
		}
	}
	return frames
}

// Ensure debug is imported for stack capture in panic recovery
var _ = debug.Stack
