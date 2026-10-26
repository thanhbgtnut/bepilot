# Feature Specification: Xử lý tài liệu bất đồng bộ và hỏi đáp trên toàn văn tài liệu

**Feature Branch**: `002-async-document-qa`

**Created**: 2026-10-26

**Status**: Draft

**Input**: User description: "Async document ingestion and document-grounded Q&A (financial reports, full text, no chunking). Upload one file → 202; background worker reads it page by page (OCR and vision model in parallel), detects number conflicts between the two readings without replacing, asks the vision model again in the same conversation to confirm only the conflicting numbers, replaces only when it is sure, otherwise marks the number unverified. Then the user asks questions on a session; the full text of every document attached to the session is given to the agent on every turn. Core agent stays unchanged."

> Feature này thay đổi hành vi của nhóm API tài liệu mô tả trong spec 001 (đọc tài liệu và hỏi về tài liệu trong cùng một yêu cầu, chờ đồng bộ). Mọi thay đổi chỉ nằm ở tầng API và thư viện đọc tài liệu. **Lõi tác tử giữ nguyên, không thêm logic riêng cho tài liệu hay cho báo cáo tài chính.**

## Bối cảnh

Người dùng chủ yếu hỏi về **báo cáo tài chính** (thường 20–100 trang, nhiều bảng số). Hiện nay hệ thống đọc file và trả lời trong một yêu cầu duy nhất, gây ra ba vấn đề:

1. File nhiều trang mất nhiều phút để đọc, client chờ quá lâu và hay bị ngắt kết nối.
2. Toàn bộ tài liệu nằm trong tin nhắn của người dùng, nên ở lượt hỏi sau nó bị cắt khỏi lịch sử và tác tử không còn thấy tài liệu.
3. Mô hình thị giác đọc chữ và bố cục tốt nhưng đôi khi đọc sai chữ số. OCR đọc số chính xác hơn, nhưng kết quả OCR không được dùng để kiểm tra số.

Quyết định thiết kế: **không chia nhỏ tài liệu (chunk)**. Báo cáo tài chính có số liệu đối chiếu chéo giữa bảng và thuyết minh, nên tác tử luôn nhận **toàn văn** tài liệu.

## User Scenarios & Testing *(mandatory)*

### Tác nhân

- **Người dùng cuối**: tải báo cáo lên và đặt câu hỏi.
- **Nhà tích hợp**: client gọi API tải lên, theo dõi tiến độ và hỏi đáp.
- **Người vận hành**: cấu hình giới hạn, theo dõi tài liệu lỗi và các số chưa xác minh.

### User Story 1 - Tải tài liệu lên và nhận kết quả đọc bất đồng bộ (Priority: P1)

Nhà tích hợp tải một file PDF hoặc ảnh lên và nhận ngay mã tài liệu, không phải chờ đọc xong. Hệ thống đọc file ở chế độ nền, từng trang một. Client xem được tiến độ (đã đọc bao nhiêu trên tổng số trang), trạng thái cuối và nội dung đã đọc của từng trang.

**Why this priority**: Đây là nền của mọi tính năng còn lại, và nó trực tiếp giải quyết vấn đề chờ lâu và bị ngắt kết nối.

**Independent Test**: Tải một PDF 50 trang lên. Kiểm tra phản hồi đến ngay (dưới 1 giây) và có mã tài liệu. Hỏi trạng thái nhiều lần, thấy số trang đã đọc tăng dần, cuối cùng là trạng thái sẵn sàng. Lấy nội dung từng trang.

**Acceptance Scenarios**:

1. **Given** một file PDF hợp lệ, **When** tải lên, **Then** hệ thống trả ngay mã tài liệu với trạng thái "chờ xử lý", chưa đọc file.
2. **Given** tài liệu đang được đọc, **When** hỏi trạng thái, **Then** thấy trạng thái "đang xử lý" cùng số trang đã đọc và tổng số trang.
3. **Given** tài liệu đã đọc xong, **When** hỏi trạng thái, **Then** thấy trạng thái "sẵn sàng", số trang lỗi, số token ước lượng và số chữ số chưa xác minh.
4. **Given** cùng một người dùng tải lại đúng file đã tải trước đó, **When** tải lên, **Then** hệ thống trả lại tài liệu cũ và không đọc lại.
5. **Given** file không phải PDF/JPEG/PNG/TIFF hoặc vượt giới hạn dung lượng, **When** tải lên, **Then** hệ thống từ chối ngay với lỗi rõ ràng.
6. **Given** tài liệu của người khác, **When** xem hoặc xoá, **Then** hệ thống trả "không tìm thấy".
7. **Given** dịch vụ khởi động lại giữa chừng, **When** chạy lại, **Then** tài liệu được đọc tiếp từ trang chưa xong, không đọc lại những trang đã có.

---

### User Story 2 - Hỏi đáp trên toàn văn tài liệu của phiên (Priority: P1)

Người dùng gắn một hoặc nhiều tài liệu vào một phiên trò chuyện, rồi hỏi. Tác tử trả lời dựa trên toàn văn mọi tài liệu của phiên và trích số trang. Ở các lượt sau, người dùng chỉ cần hỏi tiếp, không phải gửi lại file hay mã tài liệu.

**Why this priority**: Đây là giá trị chính mà người dùng cần. Nó cũng sửa lỗi tác tử mất tài liệu ở lượt sau.

**Independent Test**: Gắn một báo cáo đã sẵn sàng vào phiên và hỏi một số liệu ở trang cuối. Hỏi tiếp ba câu nữa không kèm mã tài liệu. Cả bốn câu trả lời đều dựa trên tài liệu và có số trang.

**Acceptance Scenarios**:

1. **Given** một tài liệu sẵn sàng, **When** người dùng gửi câu hỏi kèm mã tài liệu, **Then** tài liệu được gắn vào phiên và tác tử trả lời dựa trên toàn văn, có trích số trang.
2. **Given** phiên đã có tài liệu, **When** người dùng hỏi tiếp không kèm mã tài liệu, **Then** tác tử vẫn thấy toàn văn tài liệu, kể cả khi lịch sử hội thoại đã dài.
3. **Given** tài liệu của phiên còn đang xử lý và yêu cầu ở chế độ stream, **When** người dùng hỏi, **Then** kết nối được giữ, client nhận sự kiện tiến độ, và câu trả lời bắt đầu ngay khi tài liệu sẵn sàng.
4. **Given** tài liệu của phiên còn đang xử lý và yêu cầu không stream, **When** người dùng hỏi, **Then** hệ thống trả lỗi xung đột kèm tiến độ hiện tại, không gọi mô hình.
5. **Given** một tài liệu của phiên ở trạng thái lỗi, **When** người dùng hỏi, **Then** hệ thống trả lỗi nêu rõ tài liệu nào lỗi và lý do.
6. **Given** tổng kích thước tài liệu cộng lịch sử vượt khả năng của mô hình, **When** người dùng hỏi, **Then** hệ thống từ chối với lỗi nêu rõ số token cần và giới hạn, không âm thầm cắt tài liệu.
7. **Given** câu trả lời dùng một số đã được đánh dấu chưa xác minh, **When** tác tử trả lời, **Then** câu trả lời nói rõ số đó có thể bị đọc sai.
8. **Given** phiên không có tài liệu, **When** người dùng hỏi, **Then** hệ thống hoạt động đúng như trước feature này.

---

### User Story 3 - Đối chiếu chữ số giữa OCR và mô hình thị giác (Priority: P2)

Khi đọc mỗi trang, hệ thống có hai bản đọc: bản của mô hình thị giác (chữ và bố cục tốt) và bản OCR (số chính xác). Hệ thống so các chữ số giữa hai bản. Bước so sánh **chỉ đánh dấu** chỗ lệch, không tự thay. Với trang có số lệch, hệ thống hỏi lại mô hình thị giác **trong cùng cuộc hội thoại** (không phải đọc lại ảnh từ đầu) để xác nhận từng số. Số chỉ được thay khi mô hình chắc chắn. Số còn lại được ghi chú là chưa xác minh.

**Why this priority**: Báo cáo tài chính cần số liệu đúng. Tuy vậy, hệ thống vẫn dùng được (với độ tin cậy thấp hơn) khi chưa có bước này.

**Independent Test**: Dùng một trang có một số mà mô hình thị giác đọc sai còn OCR đọc đúng. Kiểm tra số được thay sau khi hỏi lại, và báo cáo đối chiếu ghi đủ giá trị trước, giá trị sau và nguồn quyết định.

**Acceptance Scenarios**:

1. **Given** hai bản đọc có cùng các chữ số, **When** đối chiếu, **Then** trang được giữ nguyên và không hỏi lại mô hình.
2. **Given** một số khác nhau giữa hai bản đọc, **When** đối chiếu, **Then** chỗ lệch được ghi vào báo cáo của trang, nội dung trang chưa thay đổi.
3. **Given** trang có chỗ lệch, **When** mô hình xác nhận chắc chắn một giá trị, **Then** đúng con số đó được thay bằng giá trị xác nhận, phần còn lại của trang không đổi.
4. **Given** trang có chỗ lệch, **When** mô hình không chắc, trả giá trị không thuộc hai bản đọc, hoặc lời gọi lỗi, **Then** số giữ nguyên và được ghi chú chưa xác minh kèm cả hai giá trị.
5. **Given** số viết theo kiểu Việt Nam (`1.234.567,89`), kiểu quốc tế (`1,234,567.89`), số âm trong ngoặc (`(1.234)`) hoặc dấu gạch thay cho số 0, **When** đối chiếu, **Then** các cách viết cùng giá trị được coi là khớp.
6. **Given** bước đối chiếu bị tắt trong cấu hình, **When** đọc tài liệu, **Then** hệ thống chạy như hiện nay.

---

### User Story 4 - Gửi file và hỏi trong một yêu cầu (tương thích ngược) (Priority: P3)

Client cũ vẫn gửi file kèm câu hỏi trong một yêu cầu. Hệ thống tạo tài liệu bất đồng bộ như User Story 1 và gắn vào phiên. Ở chế độ stream, hệ thống chờ đọc xong rồi trả lời trên cùng kết nối. Ở chế độ không stream, hệ thống trả ngay mã tài liệu và mã phiên để client theo dõi và hỏi sau.

**Why this priority**: Giữ client hiện có chạy được. Không phải giá trị mới.

**Independent Test**: Gửi một yêu cầu kèm file và câu hỏi ở chế độ stream, nhận sự kiện tiến độ rồi câu trả lời. Gửi lại ở chế độ không stream, nhận ngay mã tài liệu và mã phiên.

**Acceptance Scenarios**:

1. **Given** yêu cầu stream kèm file và câu hỏi, **When** gửi, **Then** client nhận sự kiện tiến độ, rồi sự kiện tài liệu đã đọc xong, rồi câu trả lời.
2. **Given** yêu cầu không stream kèm file và câu hỏi, **When** gửi, **Then** hệ thống trả ngay trạng thái "đã nhận" với mã tài liệu và mã phiên. Câu hỏi được trả lời khi client hỏi lại trên phiên.

---

### Edge Cases

- Một trang đọc lỗi trong một file đọc được: trang đó được lưu với lỗi, tài liệu vẫn sẵn sàng, số trang lỗi tăng lên. Tác tử được báo trang đó không đọc được.
- Mọi trang đều lỗi, hoặc PDF không mở được: hệ thống thử lại có giới hạn, sau đó chuyển tài liệu sang trạng thái lỗi kèm lý do.
- Tiến trình xử lý dừng đột ngột: sau khi hết hạn giữ việc, một tiến trình khác nhận lại tài liệu và đọc tiếp từ trang chưa xong.
- Người dùng xoá tài liệu đang xử lý: tài liệu bị xoá. Tiến trình đang đọc dừng ghi kết quả và không báo lỗi.
- Người dùng gắn tài liệu của người khác vào phiên: hệ thống từ chối như tài liệu không tồn tại.
- Phiên có nhiều tài liệu: mọi tài liệu đều được đưa cho tác tử, theo thứ tự gắn vào phiên.
- Mô hình thị giác trả về nội dung bị cắt do vượt giới hạn đầu ra: phần còn thiếu không được đối chiếu. Báo cáo của trang ghi rõ điều này.
- Hỏi trong lúc chờ quá lâu (chế độ stream): sau thời gian chờ tối đa theo cấu hình, hệ thống báo lỗi tài liệu chưa sẵn sàng.

## Requirements *(mandatory)*

### Functional Requirements

**Tiếp nhận tài liệu**

- **FR-001**: Hệ thống PHẢI nhận một file mỗi lần tải lên (PDF, JPEG, PNG, TIFF), kiểm tra loại theo nội dung file và giới hạn dung lượng, rồi trả mã tài liệu ngay mà không chờ đọc.
- **FR-002**: Hệ thống PHẢI nhận diện file trùng theo nội dung (cùng người dùng) và trả lại tài liệu đã có thay vì đọc lại. Tài liệu trùng ở trạng thái lỗi thì được đọc lại.
- **FR-003**: Khi tải lên kèm mã phiên, hệ thống PHẢI gắn tài liệu vào phiên đó, sau khi kiểm tra phiên thuộc người dùng.
- **FR-004**: Hệ thống PHẢI cho người dùng xem danh sách tài liệu của mình, xem trạng thái và tiến độ của một tài liệu, xem nội dung từng trang (có phân trang theo khoảng trang), và xoá tài liệu.
- **FR-005**: Mỗi tài liệu PHẢI có một trong các trạng thái: chờ xử lý, đang xử lý, sẵn sàng, lỗi. Tài liệu PHẢI có số trang đã đọc, tổng số trang, số trang lỗi, số token ước lượng, số chữ số chưa xác minh và thông báo lỗi (nếu có).

**Xử lý nền**

- **FR-006**: Hệ thống PHẢI đọc tài liệu bằng một nhóm tiến trình nền có số lượng cấu hình được. Mỗi tài liệu chỉ do một tiến trình xử lý tại một thời điểm.
- **FR-007**: Tiến trình PHẢI giữ quyền xử lý tài liệu bằng một thời hạn được gia hạn liên tục. Hết hạn mà không gia hạn thì tài liệu được tiến trình khác nhận lại.
- **FR-008**: Kết quả PHẢI được lưu theo từng trang ngay khi trang đọc xong. Lần xử lý lại PHẢI bỏ qua các trang đã có.
- **FR-009**: Lỗi toàn tài liệu PHẢI được thử lại tối đa số lần cấu hình được, có giãn cách tăng dần, sau đó chuyển sang trạng thái lỗi.
- **FR-010**: Với mỗi trang, hệ thống PHẢI lưu nội dung dạng markdown, văn bản OCR thô, báo cáo đối chiếu số, lỗi (nếu có) và số token ước lượng.

**Đối chiếu số**

- **FR-011**: Hệ thống PHẢI tách các chữ số trong bản markdown và bản OCR của mỗi trang, chuẩn hoá cách viết (dấu phân cách hàng nghìn và thập phân kiểu Việt Nam và quốc tế, số âm trong ngoặc, dấu gạch thay cho số 0, phần trăm), rồi căn hai dãy số theo thứ tự đọc.
- **FR-012**: Mỗi cặp số PHẢI được phân loại: khớp, lệch giá trị, chỉ có trong markdown, chỉ có trong OCR. Bước này KHÔNG được sửa nội dung trang.
- **FR-013**: Chỉ trang có số lệch hoặc số chỉ có một phía mới được hỏi lại mô hình thị giác. Lời hỏi lại PHẢI tiếp nối đúng cuộc hội thoại đọc trang ban đầu: cùng ảnh, cùng lời nhắc, cùng câu trả lời ban đầu nguyên văn, để máy chủ mô hình tái dùng được phần đã xử lý.
- **FR-014**: Mô hình PHẢI trả, cho từng chỗ lệch, giá trị đúng, nguồn (markdown, OCR hoặc khác) và mức chắc chắn. Hệ thống chỉ thay đúng con số đó khi mô hình chắc chắn và giá trị trùng với một trong hai bản đọc.
- **FR-015**: Số không được thay PHẢI giữ nguyên trong markdown và được ghi chú chưa xác minh kèm cả hai giá trị. Mọi quyết định PHẢI được lưu vào báo cáo đối chiếu của trang.
- **FR-016**: Đối chiếu số PHẢI bật/tắt được bằng cấu hình với ba mức: tắt, chỉ phát hiện, phát hiện và hỏi lại.

**Hỏi đáp**

- **FR-017**: Yêu cầu hỏi đáp PHẢI nhận thêm danh sách mã tài liệu để gắn vào phiên, sau khi kiểm tra quyền sở hữu.
- **FR-018**: Mỗi lượt hỏi trên phiên có tài liệu PHẢI đưa cho tác tử toàn văn markdown của mọi trang của mọi tài liệu thuộc phiên, theo thứ tự gắn, có đánh số trang. Nội dung này KHÔNG được lưu vào lịch sử hội thoại và KHÔNG bị cắt theo ngân sách lịch sử.
- **FR-019**: Kèm toàn văn tài liệu, tác tử PHẢI nhận chỉ dẫn: trả lời dựa trên tài liệu, trích số trang, nói rõ khi dùng số chưa xác minh, và nói rõ khi tài liệu không có thông tin.
- **FR-020**: Khi người gọi không chỉ định, độ ngẫu nhiên của mô hình cho lượt có tài liệu PHẢI đặt ở mức thấp nhất (0).
- **FR-021**: Trước khi gọi mô hình, hệ thống PHẢI ước lượng tổng token (chỉ dẫn hệ thống, tài liệu, lịch sử, đầu ra tối đa) và từ chối khi vượt giới hạn ngữ cảnh cấu hình cho mô hình. Lỗi PHẢI nêu số token cần và giới hạn.
- **FR-022**: Khi tài liệu của phiên chưa sẵn sàng: ở chế độ stream, hệ thống PHẢI giữ kết nối, gửi sự kiện tiến độ và chờ tới tối đa thời gian cấu hình; ở chế độ không stream, hệ thống PHẢI trả lỗi xung đột kèm tiến độ.
- **FR-023**: Khi một tài liệu của phiên ở trạng thái lỗi, hệ thống PHẢI từ chối lượt hỏi và nêu tài liệu lỗi.
- **FR-024**: Mã các tài liệu đã dùng cho một lượt PHẢI được ghi lại cùng lượt đó để kiểm tra về sau.
- **FR-025**: Lõi tác tử KHÔNG được thay đổi. Tài liệu đi vào tác tử qua cơ chế chỉ dẫn hệ thống theo yêu cầu mà lõi đã hỗ trợ.

**Tương thích ngược**

- **FR-026**: API gửi file kèm câu hỏi trong một yêu cầu PHẢI tiếp tục hoạt động: tạo tài liệu bất đồng bộ và gắn vào phiên, rồi ở chế độ stream thì chờ và trả lời, ở chế độ không stream thì trả ngay mã tài liệu và mã phiên.
- **FR-027**: Mọi API mới PHẢI có tài liệu API sinh từ mã nguồn như các API hiện có.

### Key Entities

- **Tài liệu (Document)**: một file người dùng tải lên. Thuộc về một người dùng. Có tên, loại, dung lượng, mã băm nội dung, trạng thái, tiến độ, số token, số trang lỗi, số chữ số chưa xác minh, lỗi, số lần thử và thời hạn giữ việc.
- **Trang tài liệu (Document Page)**: kết quả đọc một trang. Có số trang, markdown, văn bản OCR, báo cáo đối chiếu số, lỗi và số token.
- **Báo cáo đối chiếu số (Number Report)**: theo từng trang. Có số lượng số đã kiểm tra, số khớp, danh sách chỗ lệch (giá trị markdown, giá trị OCR, độ tin cậy OCR, ngữ cảnh), danh sách quyết định sau khi hỏi lại (trước, sau, nguồn, chắc chắn) và danh sách số chưa xác minh.
- **Tài liệu của phiên (Session Document)**: liên kết giữa phiên và tài liệu, có thứ tự gắn.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Tải lên một file bất kỳ (tới giới hạn dung lượng) nhận phản hồi trong dưới 1 giây, không phụ thuộc số trang.
- **SC-002**: Với một báo cáo 50 trang, client thấy tiến độ cập nhật ít nhất mỗi khi một trang đọc xong, và không có yêu cầu nào phải giữ kết nối trong lúc đọc (trừ khi client chủ động chờ ở chế độ stream).
- **SC-003**: Ở lượt hỏi thứ 10 trên cùng phiên, tác tử vẫn trả lời đúng một số liệu nằm ở trang cuối tài liệu.
- **SC-004**: Trên bộ báo cáo tài chính mẫu, tỷ lệ chữ số sai trong nội dung cuối giảm ít nhất 50% so với chỉ dùng mô hình thị giác.
- **SC-005**: 100% chữ số còn lệch sau khi hỏi lại đều được ghi chú chưa xác minh. Không có số nào bị thay mà không có xác nhận chắc chắn.
- **SC-006**: Dịch vụ khởi động lại giữa chừng không làm mất trang nào đã đọc xong, và tài liệu vẫn hoàn tất.
- **SC-007**: Khi tài liệu vượt khả năng của mô hình, 100% trường hợp nhận lỗi rõ ràng, không có trường hợp câu trả lời dựa trên tài liệu bị cắt âm thầm.

## Assumptions

- Hàng đợi việc dùng chính cơ sở dữ liệu hiện có, không thêm hạ tầng hàng đợi riêng. Nhóm tiến trình nền chạy trong cùng dịch vụ.
- File được lưu trong cơ sở dữ liệu như hiện nay (giới hạn mặc định 32 MB mỗi file).
- Máy chủ mô hình thị giác hỗ trợ tái dùng phần đầu hội thoại (prefix cache). Nếu không hỗ trợ, bước hỏi lại vẫn đúng nhưng chậm hơn.
- Bước hỏi lại chỉ áp dụng khi có mô hình thị giác. Chỉ có OCR thì đối chiếu không có ý nghĩa và bị bỏ qua.
- Giới hạn ngữ cảnh của mỗi mô hình được khai báo trong cấu hình. Không khai báo thì bỏ qua bước kiểm tra token.
- Đánh đổi đã chấp nhận: vì lõi tác tử không đổi và phần thông tin môi trường (giờ hiện tại) đứng trước chỉ dẫn theo yêu cầu, phần tài liệu không được tái dùng giữa các lượt (không có prompt caching). Mỗi lượt xử lý lại toàn văn tài liệu.
- Tài liệu vượt giới hạn ngữ cảnh của mô hình nằm ngoài phạm vi feature này. Hệ thống chỉ từ chối rõ ràng.
- Kiểm tra tổng cộng trong báo cáo tài chính (dòng tổng bằng tổng các dòng con) là logic nghiệp vụ, nằm ngoài phạm vi và không đưa vào lõi.
