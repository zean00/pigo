package ai

import "testing"

func TestRepairJSONEscapesControlAndInvalidEscapes(t *testing.T) {
	input := "\"line1\nline2\\q\""
	got := RepairJSON(input)
	want := "\"line1\\nline2\\\\q\""
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestParseJSONWithRepair(t *testing.T) {
	value, err := ParseJSONWithRepair[map[string]string]("{\"text\":\"hello\nworld\"}")
	if err != nil {
		t.Fatal(err)
	}
	if value["text"] != "hello\nworld" {
		t.Fatalf("text = %q", value["text"])
	}
}

func TestParseStreamingJSON(t *testing.T) {
	value := ParseStreamingJSON[map[string]any]("{\"a\":1,\"b\":{\"c\":2")
	b, ok := value["b"].(map[string]any)
	if !ok {
		t.Fatalf("b = %#v", value["b"])
	}
	if b["c"] != float64(2) {
		t.Fatalf("c = %#v", b["c"])
	}
}

func TestParseStreamingJSONEmptyFallback(t *testing.T) {
	value := ParseStreamingJSON[map[string]any]("{]")
	if len(value) != 0 {
		t.Fatalf("value = %#v", value)
	}
}
