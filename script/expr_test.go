package script

import "testing"

func TestExpressions(t *testing.T) {
	vals := map[string]Value{"harga": Num(12000), "jumlah": Num(3), "nama": Txt("Budi"), "a-b": Num(5), "api/db": Num(2)}
	get := func(id string) (Value, bool) { v, ok := vals[id]; return v, ok }
	cases := map[string]string{
		"harga * jumlah":                    "36000",
		"harga * jumlah * (1 - 0.1)":        "32400",
		"round(10 / 3, 2)":                  "3.33",
		"max(1, jumlah, 2) + min(4, 9)":     "7",
		"if(jumlah > 2, 10, 20)":            "10",
		"jumlah >= 3 and not (jumlah == 4)": "1",
		`"Halo " + nama`:                    `"Halo Budi"`,
		"a-b - 1":                           "4",
		"api/db / 2":                        "1",
		"-jumlah % 2":                       "-1",
		"clamp(harga, 0, 100)":              "100",
		"1_000 + 0.5":                       "1000.5",
	}
	for src, want := range cases {
		e, err := ParseExpr(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if e.String() != src {
			t.Fatalf("String() = %q", e.String())
		}
		v, verr := e.Eval(get)
		if verr != nil || v.String() != want {
			t.Fatalf("%s = %v (%v), want %s", src, v, verr, want)
		}
	}
	for src, code := range map[string]string{"1 / 0": "div_zero", "ghost + 1": "unknown_proc", "nama * 2": "bad_expr"} {
		e, _ := ParseExpr(src)
		if _, err := e.Eval(get); err == nil || err.Code != code {
			t.Fatalf("%s: %v, want %s", src, err, code)
		}
	}
	for _, bad := range []string{"", "1 +", "(1", "foo(1)", "max()", "1 2", `"open`} {
		if _, err := ParseExpr(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}
