---
name: Thẩm định phương án vay vốn
description: Rà soát hồ sơ phương án vay vốn/kinh doanh của khách hàng doanh nghiệp — kiểm tra checklist hồ sơ và trích xuất thông tin phương án thành dữ liệu có cấu trúc. Use when the user shares or references a loan/business proposal ("phương án vay vốn", "phương án kinh doanh", "phương án sử dụng vốn") and asks to check whether the file is complete, review it against a checklist, or pull out key figures (loan amount, tenor, source of repayment, collateral...) for appraisal.
---

# Thẩm định phương án vay vốn

Quy trình rà soát một hồ sơ phương án vay vốn (phương án kinh doanh / phương án
sử dụng vốn) do khách hàng doanh nghiệp nộp, gồm hai đầu việc độc lập nhưng
thường chạy nối tiếp nhau trong cùng một lượt xử lý.

## Khi nào dùng

- "Kiểm tra hồ sơ phương án vay vốn này có thiếu gì không."
- "Rà soát checklist phương án theo hồ sơ đính kèm."
- "Trích xuất thông tin phương án này giúp mình."
- "Tóm tắt các số liệu chính trong phương án vay vốn."

## Đầu việc 1 — Phân tích, kiểm tra checklist hồ sơ

Đối chiếu hồ sơ nhận được với danh mục tài liệu/nội dung bắt buộc của một
phương án vay vốn:

1. Mục đích vay & mô tả phương án kinh doanh
2. Tổng mức đầu tư & cơ cấu nguồn vốn (vốn tự có / vốn vay)
3. Kế hoạch giải ngân theo tiến độ
4. Dự kiến doanh thu — chi phí — lợi nhuận theo từng năm
5. Dòng tiền dự kiến & kế hoạch trả nợ (nguồn trả nợ, lịch trả gốc/lãi)
6. Hợp đồng đầu vào/đầu ra hoặc tài liệu chứng minh tính khả thi của phương án
7. Tài sản bảo đảm đề xuất cho phương án
8. Chữ ký, đóng dấu hợp lệ của người đại diện theo pháp luật

Với mỗi mục, kết luận một trong ba trạng thái — **Đủ**, **Thiếu một phần**
(nêu rõ phần còn thiếu), **Thiếu** (chưa có trong hồ sơ) — kèm một câu trích
dẫn vị trí trong hồ sơ khi có (tên file/trang) hoặc lý do khi không tìm thấy.
Không suy diễn nội dung không có trong hồ sơ.

## Đầu việc 2 — Trích xuất thông tin

Từ chính hồ sơ đó, trích xuất các trường dữ liệu sau thành dạng key–value:

- Tên phương án
- Bên vay / chủ đầu tư
- Mục đích vay
- Tổng mức đầu tư
- Số tiền đề nghị vay (và tỷ lệ trên tổng mức đầu tư)
- Vốn tự có tham gia
- Thời hạn vay đề xuất (kỳ hạn, thời gian ân hạn nếu có)
- Nguồn trả nợ chính
- Doanh thu / lợi nhuận dự kiến năm đầu sau đầu tư
- Tài sản bảo đảm đề xuất

Trường nào không có trong hồ sơ thì để giá trị `"chưa có trong hồ sơ"` — không
bịa số liệu. Số tiền luôn ghi rõ đơn vị (VND) và làm tròn theo đúng số liệu
gốc trong hồ sơ.

## Kết quả trả về

Trả lời gồm đúng hai khối, theo thứ tự:

1. **Checklist hồ sơ** — bảng: `Mục | Trạng thái | Ghi chú`.
2. **Thông tin trích xuất** — bảng: `Trường | Giá trị`.

Không gộp hai khối, không thêm nhận định về việc có nên cấp tín dụng hay
không — đầu việc này chỉ chuẩn bị dữ liệu cho chuyên viên thẩm định, không kết
luận thay.

## Lưu kết quả có cấu trúc

Ngay sau khi trình bày mỗi bảng ở trên, gọi tool `save_task_result` một lần
cho đầu việc đó (không gộp chung một lần gọi) — client đọc dữ liệu này để
hiển thị trên tab báo cáo dạng từng khối có thể mở/thu gọn, độc lập với cuộc
trò chuyện. `result` của MỌI đầu việc luôn có đúng 3 trường:

- `summary` — 1–2 câu tổng hợp/nhận xét chung cho đầu việc đó (ví dụ: "6/8 mục
  đủ hồ sơ; 2 mục còn thiếu chi tiết lịch trả nợ và hợp đồng đầu ra chính
  thức."). Không lặp lại nguyên văn bảng, chỉ tổng hợp.
- `result` — dữ liệu chi tiết, đúng hình dạng theo từng đầu việc (xem dưới).
- `sources` — mảng chuỗi ngắn nêu tên/phần trong hồ sơ đã dùng để có kết quả
  này (ví dụ `["Thuyết minh phương án, tr.1–2", "Bảng dự kiến doanh thu 2026–2030"]`).
  Để mảng rỗng `[]` nếu hồ sơ không có tên file/trang cụ thể để trích dẫn —
  không bịa nguồn.

Cụ thể cho từng đầu việc:

- Đầu việc 1: `task_key="checklist"`, `title="Checklist hồ sơ"`,
  `result.result={"items": [{"label": "...", "status": "ok"|"partial"|"missing", "note": "..."}]}`
  — một phần tử `items` cho mỗi mục checklist, `status` là `"ok"` (Đủ),
  `"partial"` (Thiếu một phần) hoặc `"missing"` (Thiếu); `label` và `note`
  bằng tiếng Việt đúng như trong bảng đã trình bày.
- Đầu việc 2: `task_key="extraction"`, `title="Thông tin trích xuất"`,
  `result.result={"fields": [{"label": "...", "value": "..."}]}`
  — một phần tử `fields` cho mỗi trường đã trích xuất, đúng nhãn và giá trị
  như trong bảng đã trình bày.
