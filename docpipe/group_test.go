package docpipe

import (
	"reflect"
	"testing"

	"github.com/thanhenti/bepaylot/docpipe/types"
)

func TestGroupByFirstPage(t *testing.T) {
	pt := func(n int, label string, conf float64, first bool) types.PageDocType {
		return types.PageDocType{PageNo: n, Label: label, Confidence: conf, FirstPage: first}
	}
	type seg struct {
		a, b            int
		label, proposed string
	}
	cases := []struct {
		name  string
		pages []types.PageDocType
		want  []seg
	}{
		{"không trang nào được phân loại", []types.PageDocType{pt(1, "", 0, false), pt(2, "", 0, false)}, nil},
		{"mỗi trang đầu mở một giấy tờ, trang sau nối vào; cùng loại liền nhau vẫn cắt",
			[]types.PageDocType{pt(1, "hop_dong", 0.9, true), pt(2, "hop_dong", 0.8, false), pt(3, "hop_dong", 0.9, true), pt(4, "hoa_don", 0.95, true)},
			[]seg{{1, 2, "hop_dong", ""}, {3, 3, "hop_dong", ""}, {4, 4, "hoa_don", ""}}},
		{"đổi loại mà không phải trang đầu thì không cắt; loại lấy theo trang đầu",
			[]types.PageDocType{pt(1, "hop_dong", 0.9, true), pt(2, "hoa_don", 0.95, false), pt(3, types.LabelOther, 0.9, false), pt(4, "", 0, false)},
			[]seg{{1, 4, "hop_dong", ""}}},
		{"trang đầu của file luôn mở giấy tờ, dù model không đánh dấu",
			[]types.PageDocType{pt(1, "hop_dong", 0.9, false), pt(2, "hop_dong", 0.9, false), pt(3, "hoa_don", 0.9, true)},
			[]seg{{1, 2, "hop_dong", ""}, {3, 3, "hoa_don", ""}}},
		{"trang đầu dưới ngưỡng hoặc chưa phân loại là unknown, giữ loại model đoán",
			[]types.PageDocType{pt(1, "hoa_don", 0.4, true), pt(2, "hop_dong", 0.9, false), pt(3, "", 0, false), pt(4, "hop_dong", 0.9, true)},
			[]seg{{1, 3, types.LabelUnknown, "hoa_don"}, {4, 4, "hop_dong", ""}}},
	}
	for _, c := range cases {
		var got []seg
		for _, g := range GroupByFirstPage(c.pages, 0.6) {
			got = append(got, seg{g.PageStart, g.PageEnd, g.Label, g.ProposedLabel})
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// A segment has the confidence of its first page.
	got := GroupByFirstPage([]types.PageDocType{pt(1, "hop_dong", 0.9, true), pt(2, "hop_dong", 0.2, false)}, 0.6)
	if len(got) != 1 || got[0].PageEnd != 2 || got[0].Confidence != 0.9 {
		t.Errorf("confidence: got %+v", got)
	}
}
