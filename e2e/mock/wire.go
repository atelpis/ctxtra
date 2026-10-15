package mock

import (
	"encoding/json"
	"fmt"
	"time"
)

// sse is one server-sent event; event is "" for OpenAI Chat chunks.
type sse struct {
	event string
	data  any
}

func (e sse) bytes() []byte {
	if s, ok := e.data.(string); ok { // "[DONE]"
		return []byte("data: " + s + "\n\n")
	}
	b, _ := json.Marshal(e.data)
	out := ""
	if e.event != "" {
		out = "event: " + e.event + "\n"
	}
	return []byte(out + "data: " + string(b) + "\n\n")
}

type obj = map[string]any

func argsJSON(c *call) string {
	b, _ := json.Marshal(c.args)
	return string(b)
}

// anthropic returns the whole message and its stream events.
func (s *Server) anthropic(model string, c *call, text string) (any, []sse) {
	var block obj
	stop := "end_turn"
	if c != nil {
		block = obj{"type": "tool_use", "id": fmt.Sprintf("toolu_mock%d", s.nextCallID()), "name": c.name, "input": c.args}
		stop = "tool_use"
	} else {
		block = obj{"type": "text", "text": text}
	}
	msg := obj{"id": "msg_mock", "type": "message", "role": "assistant", "model": model, "content": []any{block},
		"stop_reason": stop, "stop_sequence": nil, "usage": obj{"input_tokens": 1, "output_tokens": 1}}

	start := obj{}
	for k, v := range msg {
		start[k] = v
	}
	start["content"], start["stop_reason"] = []any{}, nil
	ev := []sse{{"message_start", obj{"type": "message_start", "message": start}}}
	if c != nil {
		ev = append(ev,
			sse{"content_block_start", obj{"type": "content_block_start", "index": 0,
				"content_block": obj{"type": "tool_use", "id": block["id"], "name": c.name, "input": obj{}}}},
			sse{"content_block_delta", obj{"type": "content_block_delta", "index": 0,
				"delta": obj{"type": "input_json_delta", "partial_json": argsJSON(c)}}})
	} else {
		ev = append(ev,
			sse{"content_block_start", obj{"type": "content_block_start", "index": 0, "content_block": obj{"type": "text", "text": ""}}},
			sse{"content_block_delta", obj{"type": "content_block_delta", "index": 0, "delta": obj{"type": "text_delta", "text": text}}})
	}
	ev = append(ev,
		sse{"content_block_stop", obj{"type": "content_block_stop", "index": 0}},
		sse{"message_delta", obj{"type": "message_delta", "delta": obj{"stop_reason": stop, "stop_sequence": nil},
			"usage": obj{"output_tokens": 1}}},
		sse{"message_stop", obj{"type": "message_stop"}})
	return msg, ev
}

// chat returns the whole OpenAI chat completion and its stream chunks.
func (s *Server) chat(model string, c *call, text string) (any, []sse) {
	created := time.Now().Unix()
	usage := obj{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2}
	var message, delta obj
	finish := "stop"
	if c != nil {
		tc := obj{"id": fmt.Sprintf("call_mock%d", s.nextCallID()), "type": "function",
			"function": obj{"name": c.name, "arguments": argsJSON(c)}}
		message = obj{"role": "assistant", "content": nil, "tool_calls": []any{tc}}
		indexed := obj{"index": 0}
		for k, v := range tc {
			indexed[k] = v
		}
		delta = obj{"role": "assistant", "content": nil, "tool_calls": []any{indexed}}
		finish = "tool_calls"
	} else {
		message = obj{"role": "assistant", "content": text}
		delta = message
	}
	whole := obj{"id": "chatcmpl-mock", "object": "chat.completion", "created": created, "model": model, "usage": usage,
		"choices": []any{obj{"index": 0, "finish_reason": finish, "message": message}}}
	chunk := func(choice obj, extra obj) obj {
		o := obj{"id": "chatcmpl-mock", "object": "chat.completion.chunk", "created": created, "model": model,
			"choices": []any{choice}}
		for k, v := range extra {
			o[k] = v
		}
		return o
	}
	return whole, []sse{
		{"", chunk(obj{"index": 0, "delta": delta, "finish_reason": nil}, nil)},
		{"", chunk(obj{"index": 0, "delta": obj{}, "finish_reason": finish}, obj{"usage": usage})},
		{"", "[DONE]"},
	}
}

// responses returns the whole OpenAI response and its stream events.
func (s *Server) responses(model string, c *call, text string) (any, []sse) {
	usage := obj{"input_tokens": 1, "input_tokens_details": obj{"cached_tokens": 0},
		"output_tokens": 1, "output_tokens_details": obj{"reasoning_tokens": 0}, "total_tokens": 2}
	var item obj
	if c != nil {
		n := s.nextCallID()
		item = obj{"type": "function_call", "id": fmt.Sprintf("fc_mock%d", n), "call_id": fmt.Sprintf("call_mock%d", n),
			"name": c.name, "arguments": argsJSON(c), "status": "completed"}
		if c.namespace != "" {
			item["namespace"] = c.namespace
		}
	} else {
		item = obj{"type": "message", "id": "msg_mock", "role": "assistant", "status": "completed",
			"content": []any{obj{"type": "output_text", "text": text, "annotations": []any{}}}}
	}
	resp := func(status string, output []any) obj {
		o := obj{"id": "resp_mock", "object": "response", "created_at": time.Now().Unix(), "model": model,
			"status": status, "output": output}
		if status == "completed" {
			o["usage"] = usage
		}
		return o
	}
	with := func(o obj, kv ...any) obj {
		out := obj{}
		for k, v := range o {
			out[k] = v
		}
		for i := 0; i < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		return out
	}
	seq := 0
	ev := func(kind string, kv ...any) sse {
		o := with(obj{"type": kind, "sequence_number": seq}, kv...)
		seq++
		return sse{kind, o}
	}
	events := []sse{ev("response.created", "response", resp("in_progress", []any{}))}
	if c != nil {
		id := item["id"]
		events = append(events,
			ev("response.output_item.added", "output_index", 0, "item", with(item, "arguments", "", "status", "in_progress")),
			ev("response.function_call_arguments.delta", "item_id", id, "output_index", 0, "delta", item["arguments"]),
			ev("response.function_call_arguments.done", "item_id", id, "output_index", 0, "arguments", item["arguments"]))
	} else {
		part := obj{"type": "output_text", "text": text, "annotations": []any{}}
		events = append(events,
			ev("response.output_item.added", "output_index", 0, "item", with(item, "status", "in_progress", "content", []any{})),
			ev("response.content_part.added", "item_id", "msg_mock", "output_index", 0, "content_index", 0, "part", with(part, "text", "")),
			ev("response.output_text.delta", "item_id", "msg_mock", "output_index", 0, "content_index", 0, "delta", text),
			ev("response.output_text.done", "item_id", "msg_mock", "output_index", 0, "content_index", 0, "text", text),
			ev("response.content_part.done", "item_id", "msg_mock", "output_index", 0, "content_index", 0, "part", part))
	}
	events = append(events,
		ev("response.output_item.done", "output_index", 0, "item", item),
		ev("response.completed", "response", resp("completed", []any{item})))
	return resp("completed", []any{item}), events
}
