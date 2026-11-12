package values

import (
	"strings"
	"testing"
)

func TestConvertFull(t *testing.T) {
	long := strings.Repeat("é", MaxText)
	if c := ConvertFull(long, CatText); c.Trunc || c.V != long {
		t.Fatal("text cut")
	}
	if c := ConvertCat(long, CatText); !c.Trunc {
		t.Fatal("display conversion must cut")
	}
	big := make([]byte, MaxBytes+1)
	if c := ConvertFull(big, CatBinary); c.Trunc || len(c.V.(string)) != 2*len(big) {
		t.Fatal("bytes cut")
	}
	if c := ConvertFull([]byte(long), CatText); c.Trunc {
		t.Fatal("[]byte text cut")
	}
}
