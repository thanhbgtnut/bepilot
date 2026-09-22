# Trợ lý Thẩm định Doanh nghiệp

Dự án minh họa khái niệm **tác tử AI gắn theo vai trò nghiệp vụ** (*role-based agent*)
áp dụng cho công tác **thẩm định tín dụng doanh nghiệp** trong ngân hàng.

> ⚠️ **Đây là prototype minh họa khái niệm.** Toàn bộ số liệu, tên doanh nghiệp và hồ sơ
> (Công ty CP Thực phẩm Đồng Xanh, hồ sơ `LOS-2026-04871`, đề nghị cấp tín dụng 25 tỷ VND)
> đều là **hư cấu**. Không dùng cho quyết định tín dụng thực tế.

---

## 1. Diễn giải dự án

### Vấn đề

Một hồ sơ đề nghị cấp tín dụng doanh nghiệp đi qua quy trình khởi tạo khoản vay (LOS —
*Loan Origination System*) gồm nhiều bước: tiếp nhận → thẩm định → chuyên viên rà soát →
phê duyệt → giải ngân. Bước **thẩm định** ngốn nhiều giờ tác nghiệp lặp đi lặp lại: bóc
tách báo cáo tài chính, tính chỉ số, tra cứu CIC, rà soát pháp lý, phân tích ngành, dựng
tờ trình... phần lớn là công việc chuẩn bị và phân tích sơ bộ trước khi con người ra phán quyết.

### Ý tưởng: tác tử gắn theo vai trò

Thay vì xây một chatbot hỏi–đáp chung chung, dự án mô hình hóa AI như **một nhân sự có vai
trò cụ thể** trong tổ chức — ở đây là *"Chuyên viên thẩm định doanh nghiệp, bậc Junior"*:

- **Có phạm vi trách nhiệm rõ ràng** — làm phần việc chuẩn bị và phân tích sơ bộ; *không*
  kết luận cấp tín dụng, *không* phê duyệt. Mọi quyết định vẫn thuộc chuyên viên thẩm định
  và cấp có thẩm quyền của ngân hàng.
- **Làm việc trên hệ thống nghiệp vụ thật (LOS)** thông qua một bộ "công cụ" (tool) được
  định nghĩa theo nghiệp vụ: đọc hồ sơ, tra CIC, ghi tờ trình nháp vào hồ sơ, tạo yêu cầu
  bổ sung tài liệu...
- **Minh bạch** — mỗi đầu việc đều kèm **độ tin cậy**, **nguồn dữ liệu** và **các điểm cần
  chuyên viên xác nhận**; những nội dung nhạy cảm (giao dịch bên liên quan, vượt thẩm quyền,
  chênh lệch số liệu) được đánh dấu để con người xử lý.
- **Tích lũy kinh nghiệm** — mỗi phản hồi của chuyên viên được đưa lại vào bộ quy tắc và ví
  dụ tham chiếu, nâng dần "bậc" và độ chính xác của Trợ lý so với kết luận của chuyên gia.

### Sản phẩm trong repo này

Dự án gồm hai phần bổ trợ nhau:

| Phần | Vai trò |
| --- | --- |
| **`chat-widget/`** | Widget chat nhúng được vào trang LOS hiện có. Đây là *giao diện làm việc* giữa chuyên viên và Trợ lý: xem tóm tắt, đọc báo cáo đầy đủ, trao đổi và giao thêm đầu việc. Kết nối tới backend tác tử qua giao thức **AG-UI**. |
| **`chat-widget/prototype/`** | Các bản mock HTML tĩnh mô tả tầm nhìn: bảng làm việc đầy đủ của Trợ lý, màn hình LOS có nhúng widget, và sơ đồ kiến trúc Agent ⇄ LOS. |

---

## 2. Cấu trúc thư mục

```
trolythamdinh/
└─ chat-widget/
   ├─ src/
   │  ├─ App.tsx                 # Trang host giả lập (chỉ để xem thử widget khi dev)
   │  ├─ mount.tsx               # mountAppraisalAgentWidget() — gắn widget vào Shadow DOM
   │  ├─ embed/embed.ts          # Entry của script nhúng: đọc data-* rồi gọi mount
   │  └─ widget/
   │     ├─ AgentDock.tsx        # Nút nổi (FAB) + callout nhắc + panel
   │     ├─ panel/               # Panel 3 tab: Trao đổi / Báo cáo đầy đủ / Lịch sử
   │     │  └─ chat/             # Khung chat: bong bóng tin nhắn, ô soạn, quick replies, form giao việc
   │     └─ agent/               # Cầu nối AG-UI (useAgentChat), lịch sử session, báo cáo (GET /v1/sessions/{id}/report)
   ├─ prototype/
   │  ├─ appraisal-agent.html         # Bảng làm việc đầy đủ của Trợ lý cho 1 hồ sơ
   │  ├─ los-embedded.html            # Màn hình hồ sơ LOS có nhúng widget
   │  └─ agent-los-architecture.html  # Sơ đồ kiến trúc & luồng vận hành Agent ⇄ LOS
   ├─ mock-agent-server.cjs      # Mock server AG-UI cho dev, không dùng cho production
   ├─ dist/                      # Kết quả build: appraisal-agent-widget.js (1 file nhúng)
   └─ package.json
```

---

## 3. `chat-widget` — widget nhúng

### Công nghệ

- **React 19** + **TypeScript**, build bằng **Vite 8** (chế độ `build.lib` → 1 file IIFE).
- **Tailwind CSS v4** cho style widget.
- **[`@ag-ui/client`](https://github.com/ag-ui-protocol/ag-ui)** — giao thức AG-UI, streaming
  hội thoại và tool call giữa UI và backend tác tử qua HTTP/SSE.
- Widget tự tạo **Shadow DOM** riêng → CSS không xung đột với trang LOS chủ.

### Chạy thử (dev)

Từ thư mục gốc repo (`bepilot/`), chạy cả agent (cổng 9090) và frontend cùng lúc:

```bash
make run-web
```

Lệnh trên gọi `scripts/run-web.sh`, tự khởi động agent bepilot thật (Postgres qua
`make up`, rồi `go run ./cmd/server` trên `:9090`) và frontend (`npm run dev`, cổng
mặc định của Vite — 5173, hoặc cổng kế tiếp nếu 5173 đang bận). Ctrl+C dừng cả hai.

Widget mặc định kết nối tới `http://localhost:9090/v1/ag-ui/run` (route AG-UI của
agent bepilot). Đổi endpoint bằng biến môi trường khi chạy dev riêng lẻ:

```bash
cd frontend
VITE_AGENT_URL=http://localhost:8787/agent npm run dev   # dùng lại mock cũ
npm run mock-agent                                        # mock AG-UI (cổng 8787)
```

Trang `npm run dev` chỉ là **host giả lập** (`src/App.tsx`) để chứng minh widget gắn vào
sạch sẽ bên cạnh nội dung bất kỳ — không phải bản dựng lại giao diện LOS thật.

### Build ra file nhúng

```bash
cd chat-widget
npm run build         # → dist/appraisal-agent-widget.js
```

### Nhúng vào trang LOS thật

```html
<script
  src="https://cdn.example.com/appraisal-agent-widget.js"
  data-loan-id="LOS-2026-04871"
  data-borrower-name="Công ty CP Thực phẩm Đồng Xanh"
  data-agent-url="https://agents.example.com/appraisal-agent"
  data-agent-auth-token="..."
  async
></script>
```

Các thuộc tính `data-*` được `src/embed/embed.ts` đọc và ánh xạ sang `MountOptions`
(xem `src/widget/types.ts` cho danh sách đầy đủ: `agentName`, `agentRole`, `userInitials`,
`pendingCount`, `autoOpenDelay`, `nudgeDelay`...). `data-agent-url` phải trỏ tới **backend
tác tử thật** tuân thủ AG-UI; `mock-agent-server.cjs` chỉ chứng minh đúng "dây" giao thức.

### Ba tab của panel

| Tab | Nội dung |
| --- | --- |
| **Trao đổi** | Chat trực tiếp với Trợ lý qua AG-UI; có quick replies và form giao thêm đầu việc. Tool call của tác tử được render thành thẻ (tên tool + tham số). |
| **Báo cáo đầy đủ** | Kết quả các đầu việc Trợ lý đã lưu cho phiên hiện tại — đọc từ `GET /v1/sessions/{id}/report` (bảng Postgres `task_results`, ghi bởi tool `save_task_result`), **không** đi qua agent, nên vẫn hiển thị được ngay cả khi một lượt chạy agent đang lỗi. Render theo hình dạng JSON của từng kết quả (`items` → checklist, `fields` → bảng key–value, còn lại → JSON thô), không gắn cứng theo `task_key` của riêng skill nào. |
| **Lịch sử** | Danh sách các phiên trò chuyện trước đó (`GET /v1/sessions`); chọn một phiên để nạp lại transcript và tiếp tục. |

---

## 4. `prototype/` — các bản mock tầm nhìn

Đều là **HTML một file, không cần build** — mở trực tiếp bằng trình duyệt. Hỗ trợ sáng/tối
theo hệ thống và responsive.

### `appraisal-agent.html` — Bảng làm việc của Trợ lý

Màn hình đầy đủ trình bày **những phần việc Trợ lý đã tự động xử lý** cho một hồ sơ vay:

- **Dải bối cảnh** — bên vay, đề nghị cấp tín dụng, tiến trình hồ sơ 5 bước.
- **4 ô tổng quan** — đầu việc hoàn tất, thời gian tiết kiệm, nội dung cần xác nhận, mức sẵn sàng tờ trình.
- **Nhật ký 9 đầu việc thẩm định** (bấm từng dòng để mở chi tiết):
  1. Trích xuất & chuẩn hóa BCTC 2023–2025 (spreading)
  2. Phân tích chỉ số tài chính & khả năng trả nợ (DSCR)
  3. Tra cứu CIC & lịch sử quan hệ tín dụng
  4. Rà soát hồ sơ pháp lý & thẩm quyền vay vốn
  5. Phân tích ngành & vị thế cạnh tranh
  6. Kiểm tra dấu hiệu cảnh báo sớm
  7. Định giá sơ bộ tài sản bảo đảm
  8. Dự thảo tờ trình thẩm định & đề xuất cấu trúc khoản vay
  9. Kiểm tra tuân thủ giới hạn cấp tín dụng *(đang chờ dữ liệu)*

  Mỗi đầu việc có thanh **độ tin cậy**, **trạng thái** (Hoàn thành / Cần xác nhận / Chờ dữ liệu),
  phần Kết quả, Lưu ý cho chuyên viên, Nguồn dữ liệu và các nút hành động mẫu.
- **Sidebar** — hồ sơ năng lực Trợ lý (bậc kinh nghiệm, thành thạo theo kỹ năng), độ chính
  xác so với chuyên gia (kèm sparkline xu hướng), nhật ký hoạt động trên hồ sơ.

Kỹ thuật: CSS inline; JS ~8 dòng chỉ để mở/đóng đầu việc; tôn trọng `prefers-reduced-motion`;
bố cục 2 cột → 1 cột ở màn hình ≤ 980px.

### `los-embedded.html` — LOS có nhúng widget

Màn hình hồ sơ tín dụng `LOS-2026-04871` của hệ thống LOS, minh họa vị trí widget Trợ lý
xuất hiện bên cạnh nội dung nghiệp vụ.

### `agent-los-architecture.html` — Kiến trúc Agent ⇄ LOS

Tài liệu kiến trúc: nguyên tắc nền tảng, kiến trúc phân lớp, luồng vận hành end-to-end,
nguyên tắc thiết kế tool của LOS, danh mục tool nghiệp vụ, module Tuân thủ (Compliance MCP),
sơ đồ kết nối Agent ⇄ Tool, và một luồng minh họa (phân tích báo cáo tài chính).

---

## 5. Ranh giới trách nhiệm

Trợ lý Thẩm định thực hiện phần việc **chuẩn bị và phân tích sơ bộ**. Mọi **kết luận cấp
tín dụng** và **quyết định phê duyệt** vẫn do chuyên viên thẩm định và cấp có thẩm quyền
của ngân hàng chịu trách nhiệm.
