package textutil

import "testing"

func TestValueKey(t *testing.T) {
	eq := [][2]string{
		{"15.000.000.000", "15000000000"},
		{"0101-234-567", "0101234567"},
		{"Trần Minh Khoa", "tran minh  khoa"},
		{"QSDĐ thửa 112", "qsdd thua 112"},
	}
	for _, c := range eq {
		if ValueKey(c[0]) != ValueKey(c[1]) {
			t.Errorf("%q and %q should compare equal: %q vs %q", c[0], c[1], ValueKey(c[0]), ValueKey(c[1]))
		}
	}
	if ValueKey("6.214.800.000") == ValueKey("6.124.800.000") {
		t.Error("different numbers compare equal")
	}
	if !ValueIn("6.124.800.000", "60. Lợi nhuận sau thuế TNDN … 6.124.800.000") {
		t.Error("number not found in its quote")
	}
	if ValueIn("6.214.800.000", "60. Lợi nhuận sau thuế TNDN … 6.124.800.000") {
		t.Error("wrong number found in quote")
	}
	if !ValueIn("Dịch Vọng Hậu", "phường Dịch Vọng Hậu, quận Cầu Giấy") {
		t.Error("text not found in its quote")
	}
}
