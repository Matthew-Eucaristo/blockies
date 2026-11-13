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

func TestNewStatementsRoundTrip(t *testing.T) {
	src := "click vote/a: vote/a -> tally : vote; add tally 1; body tally +1; focus tally\ntap vote/a\n& tap vote/b\ntap vote/a 12 vote/b 5 vote/c\nadd tally -3\nbody tally -2\nbody tally 4\nfocus stage\n"
	s, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := Format(s); got != src {
		t.Fatalf("round trip:\n%s\nwant\n%s", got, src)
	}
	for _, bad := range []string{"on tally vote: tap vote/a", "add tally", "add tally 1 +", "tap", "body tally +65", "click : x -> y", "tap vote/a 0", "tap vote/a 10001", "tap 3"} {
		if _, errs := Parse(bad); len(errs) != 1 {
			t.Fatalf("%q parsed without error", bad)
		}
	}
}

func TestKeywordNamedProcesses(t *testing.T) {
	src := "proc add\nproc tap\nadd -> tap : hi\ntap = busy\nnote add \"x\"\n"
	s, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if s.Stmts[2].Op != Send || s.Stmts[3].Op != State {
		t.Fatalf("ops = %s %s", s.Stmts[2].Op, s.Stmts[3].Op)
	}
	if Format(s) != src {
		t.Fatalf("round trip: %q", Format(s))
	}
}

func TestValuesAndFormulasRoundTrip(t *testing.T) {
	src := `proc harga "Harga" skin=input value 12000
proc jumlah "Jumlah" skin=slider min=0 max=100 value 3
proc nama skin=input value "Budi Santoso"
proc total "Total" skin=number
calc total = harga * jumlah
set total round(total / 3, 2)
add total 1
cap db kapasitas
body tank level / 10
move api 4 6
`
	s, errs := Parse(src)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if got := Format(s); got != src {
		t.Fatalf("round trip:\n%s\nwant\n%s", got, src)
	}
	for _, bad := range []string{"proc x skin=fancy", "proc x min=a", "proc x value", "calc = 1", "calc x 1", "set x", "move x", "move x a b", "set x 1 +"} {
		if _, errs := Parse(bad); len(errs) != 1 {
			t.Fatalf("%q parsed without error", bad)
		}
	}
}
