package script

import (
	"reflect"
	"testing"
)

const sample = `# checkout, as the agent tells it
world 24 12
kind db color=green states=idle,busy,down

proc client "Browser" color=blue
proc api "API \"edge\"" kind=service
proc db kind=db at 20 6
client -> api : GET /cart
& client -> api
api -> db : SELECT cart
db = busy
note db "3 rows -> cached"
body db 3
wait 2
cap db 1
on db SELECT: db -> $from : rows; db = idle
every 4: client -> api : GET /cart
proc api/cache
end api/cache
`

func TestRoundTrip(t *testing.T) {
	s, errs := Parse(sample)
	if len(errs) > 0 {
		t.Fatalf("errors: %v", errs)
	}
	if got := Format(s); got != sample {
		t.Fatalf("format changed the script:\n%s", got)
	}
	again, errs := Parse(Format(s))
	if len(errs) > 0 || !reflect.DeepEqual(again, s) {
		t.Fatalf("parse(format(s)) != s: %v", errs)
	}
}

func TestBadLineIsReportedAndTheRestLoads(t *testing.T) {
	s, errs := Parse("proc a\nproc b\nthis is not a statement\na -> b : hi\nwait zero\n")
	if len(errs) != 2 || errs[0].Line != 3 || errs[0].Code != "syntax" || errs[1].Line != 5 || errs[1].Code != "bad_number" {
		t.Fatalf("errors = %v", errs)
	}
	if len(s.Stmts) != 3 || s.Stmts[2].Op != Send || s.Stmts[2].Line != 4 {
		t.Fatalf("stmts = %+v", s.Stmts)
	}
}

func TestErrorCodes(t *testing.T) {
	cases := map[string]string{
		"world 0 4":                     "bad_world",
		"world 1 2 3 4 5":               "bad_world",
		"kind db color=mauve":           "bad_color",
		"kind db states=a,a":            "bad_state",
		"proc 9lives":                   "bad_id",
		"proc a \"unclosed":             "syntax",
		"a = two words":                 "bad_state",
		"body a 999":                    "bad_number",
		"cap a 0":                       "bad_number",
		"on a x: wait 2":                "bad_action",
		"on a x:":                       "bad_action",
		"every 2: world 3":              "bad_action",
		"& wait 2":                      "syntax",
		"a -> : nothing":                "syntax",
		"teleport a b":                  "syntax",
		"$from -> a : top level ok":     "",
		"note a \"x -> y is not sent\"": "",
	}
	for line, want := range cases {
		_, err := ParseLine(line)
		got := ""
		if err != nil {
			got = err.Code
		}
		if got != want {
			t.Errorf("%q: code %q, want %q", line, got, want)
		}
	}
}

func TestNoteWithArrowIsANote(t *testing.T) {
	st, err := ParseLine(`note db "a -> b"`)
	if err != nil || st.Op != Note || st.Text != "a -> b" {
		t.Fatalf("%+v %v", st, err)
	}
}
