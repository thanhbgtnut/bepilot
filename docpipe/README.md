# docpipe

Thư viện Go đóng gói luồng xử lý tài liệu của bepaylot để dùng ở project khác:

```
PDF ─► ảnh từng trang ─► phân loại từng trang (loại tài liệu + is_first_page)
    ─► gom trang thành tài liệu theo trang đầu
    ─► OCR từng trang (TurboOCR /ocr/raw) ─► VLM đọc lại trang thành markdown
```

Module không dùng database, object storage hay queue. Đầu vào là đường dẫn file PDF, đầu ra nằm trong bộ nhớ: ảnh trang, loại của từng trang, danh sách tài liệu đã gom, dòng chữ có toạ độ và markdown của từng trang.

## Cài đặt

```
go get github.com/thanhenti/bepaylot/docpipe
```

Module nằm trong thư mục con của repo bepaylot, nên phiên bản được đánh tag dạng `docpipe/v0.1.0`. Repo private thì đặt `GOPRIVATE=github.com/thanhenti/*`. Khi làm việc trên cùng máy, có thể trỏ thẳng vào thư mục:

```
replace github.com/thanhenti/bepaylot/docpipe => ../bepaylot/docpipe
```

Yêu cầu Go 1.26. Chế độ render mặc định (`webassembly`) không cần cgo hay libpdfium.

## Dùng

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/thanhenti/bepaylot/docpipe"
	"github.com/thanhenti/bepaylot/docpipe/decision"
	"github.com/thanhenti/bepaylot/docpipe/parser/pdf"
	"github.com/thanhenti/bepaylot/docpipe/parser/turboocr"
	"github.com/thanhenti/bepaylot/docpipe/parser/vlm"
	"github.com/thanhenti/bepaylot/docpipe/types"
)

func main() {
	// 1. PDF → ảnh trang
	renderer, err := pdf.New(pdf.Config{Mode: "webassembly", Workers: 2})
	if err != nil {
		log.Fatal(err)
	}
	defer renderer.Close()

	// 2. Model phân loại (Decision API, POST /v1/systemone)
	classifier := decision.New(decision.Config{
		BaseURL: "http://localhost:11434", Model: "ten-model-phan-loai",
		Instructions: "Đây là loại chứng từ nào?", Timeout: time.Minute, MaxSide: 1288,
	})

	// 3. OCR /ocr/raw, rồi VLM đọc lại từng trang
	ocr := turboocr.New(turboocr.Config{BaseURL: "http://10.215.122.20:30189", Timeout: 2 * time.Minute})
	engine, err := vlm.New(vlm.Config{
		Layout: ocr,
		Client: vlm.NewOpenAI(vlm.OpenAIConfig{BaseURL: "http://localhost:1234/v1", Model: "allenai/olmocr-2-7b"}),
	})
	if err != nil {
		log.Fatal(err)
	}

	p, err := docpipe.New(docpipe.Config{
		Renderer:   renderer,
		Classifier: classifier,
		Labels: []types.ClassLabel{
			{Name: "hop_dong", Title: "Hợp đồng"},
			{Name: "hoa_don", Title: "Hoá đơn", Description: "hoá đơn giá trị gia tăng"},
		},
		Engine: engine,
	})
	if err != nil {
		log.Fatal(err)
	}

	res, err := p.Run(context.Background(), "ho-so.pdf")
	if err != nil {
		log.Fatal(err)
	}
	for _, d := range res.Documents {
		fmt.Printf("%s: trang %d–%d (độ tin cậy %.2f)\n", d.Label, d.PageStart, d.PageEnd, d.Confidence)
	}
	for _, pg := range res.Pages {
		if pg.OCRErr != nil {
			fmt.Printf("trang %d lỗi OCR: %v\n", pg.PageNo, pg.OCRErr)
			continue
		}
		fmt.Printf("--- trang %d (%s)\n%s\n", pg.PageNo, pg.Type.Label, pg.Parsed.Markdown)
	}
}
```

Chỉ cần OCR, không cần VLM: truyền `Engine: ocr`. Không cần phân loại: bỏ `Classifier`, khi đó `res.Documents` rỗng.

## Kết quả

`Run` trả `*docpipe.Result`:

| Trường | Nội dung |
|---|---|
| `Pages[i].JPEG`, `Width`, `Height` | ảnh trang đã render |
| `Pages[i].Type` | câu trả lời của model phân loại: `Label`, `Confidence`, `FirstPage` |
| `Pages[i].Parsed` | `types.ParsedPage`: `Lines` (text, toạ độ, độ tin cậy), `Blocks` (tiêu đề, đoạn, bảng HTML…), `Markdown` |
| `Pages[i].Raw` | JSON gốc của engine (response `/ocr/raw`, kèm lần gọi VLM nếu có) |
| `Pages[i].ClassifyErr`, `OCRErr` | lỗi của riêng trang đó |
| `Documents[j]` | một tài liệu: `PageStart`, `PageEnd`, `Label`, `ProposedLabel`, `Confidence` |

Một trang lỗi phân loại hoặc lỗi OCR không làm hỏng cả lần chạy: lỗi nằm trên trang đó. `Run` chỉ trả lỗi khi không render được PDF hoặc `ctx` bị huỷ.

## Quy tắc gom tài liệu

Mỗi trang được hỏi hai câu trong cùng một request: `document_type` (chọn trong `Labels`, luôn có thêm `other`) và `is_first_page` (`YES` / `NO`).

- Trang `is_first_page = YES` mở một tài liệu mới; các trang sau nối vào tài liệu đó cho tới trang đầu kế tiếp.
- Trang đầu của file luôn mở một tài liệu.
- Loại và độ tin cậy của tài liệu lấy theo trang đầu của nó. Trang đầu có độ tin cậy dưới `MinConfidence` (mặc định 0.6) hoặc chưa phân loại thì tài liệu là `unknown`, loại model đoán được giữ ở `ProposedLabel`.
- Trang lỗi phân loại không phải trang đầu, nên nối vào tài liệu trước nó.
- Không trang nào được phân loại thì không có tài liệu nào.

Hàm `docpipe.GroupByFirstPage` là hàm thuần, dùng riêng được khi đã có loại của từng trang.

## Thứ tự chạy

`Run` chạy ba bước nối tiếp: render hết các trang, phân loại hết các trang rồi gom, sau đó OCR (và VLM) hết các trang. Phân loại và OCR không bao giờ chạy cùng lúc, nên một server chỉ nạp được một model mỗi lúc vẫn dùng được. Trong mỗi bước, `Concurrency` trang chạy song song (mặc định 4).

## Thay thành phần

| Thành phần | Interface | Có sẵn |
|---|---|---|
| Phân loại trang | `docpipe.Classifier` | `decision.Client` (Decision API của Ollama) |
| OCR / layout | `parser.Engine` | `turboocr.Client` (`POST /ocr/raw`), `vlm.Engine` (TurboOCR + VLM) |
| Gọi VLM | `vlm.Transcriber` | `vlm.NewOpenAI` (API kiểu OpenAI: LM Studio, OpenRouter, Groq…) |

Tham số gửi sang `/ocr/raw` nằm ở `Config.Options` (`parser.PageOptions`). Mặc định bật `autorotate`, `layout`, `reading_order`, `tables` và bước VLM.

## Giới hạn

- Chỉ nhận PDF. File ảnh: dùng `parser/imagefile` để chuẩn hoá ảnh rồi tự gọi `Classifier` và `Engine`.
- Không dùng text layer của PDF: trang nào cũng đi qua OCR. (bepaylot ghép text layer ở tầng ứng dụng bằng `parser/textlayer`.)
- `autorotate` làm TurboOCR xoay ảnh trước khi đọc nhưng không trả góc xoay: toạ độ trả về là của ảnh đã xoay, còn `Pages[i].JPEG` vẫn là ảnh gốc.
- Chế độ render `multi_threaded` (nhanh hơn, cần libpdfium) cần binary `pdfium-worker`; mã nguồn của nó ở `cmd/pdfium-worker` trong repo bepaylot.

## Test

```
go test ./...
```

`TestRun` chạy cả luồng trên một PDF thật với server OCR và VLM giả, không cần mạng.
