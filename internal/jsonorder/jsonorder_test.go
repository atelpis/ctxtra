package jsonorder

import "testing"

func TestRoundTrip(t *testing.T) {
	in := `{"z":1.50,"a":{"y":"<&>","b":[1,{"k":null}]},"m":true}`
	v, err := Decode([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Marshal(v)
	if err != nil || string(out) != in {
		t.Errorf("got %s (%v), want %s", out, err, in)
	}
	m := v.(*Map)
	m.Delete("a")
	m.Set("z", 2)
	m.Set("n", "x")
	if out, _ := Marshal(m); string(out) != `{"z":2,"m":true,"n":"x"}` {
		t.Errorf("after edits: %s", out)
	}
	for _, bad := range []string{`{"a":1} {}`, `{"a":`} {
		if _, err := Decode([]byte(bad)); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

func TestDecodeRejectsDuplicateKeys(t *testing.T) {
	if _, err := Decode([]byte(`{"hooks": {}, "a": {"x": 1, "x": 2}}`)); err == nil {
		t.Error("duplicate key accepted")
	}
}
