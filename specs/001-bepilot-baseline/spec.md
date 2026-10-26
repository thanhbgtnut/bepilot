# Feature Specification: BePilot Baseline (hệ thống hiện có)

**Feature Branch**: `001-bepilot-baseline`

**Created**: 2026-10-26

**Status**: Draft

**Input**: User description: "Baseline specification of the existing BePilot system (document what it already does, not a new feature). BePilot is a streaming AI agent backend that is use-case-agnostic: any business domain is added through skills and tools, never hard-coded into the core agent. […] Out of scope: the docpipe document library and the appraisal frontend widget, which get their own specs."

> Spec này mô tả **hành vi đang có** của BePilot (tính đến commit `cc47ba5`), dùng làm mốc cho các feature sau. Mọi yêu cầu dưới đây đã được hiện thực; feature mới thay đổi hành vi nào ở đây thì phải ghi rõ trong spec của nó.

## Nguyên tắc nền

BePilot là **lõi tác tử dùng chung, không gắn nghiệp vụ**. Mọi nghiệp vụ (thẩm định tín dụng, điền form PDF, làm sạch dữ liệu…) được thêm bằng *skill* (tài liệu hướng dẫn) và *tool* (công cụ ngoài). Lõi không được chứa logic, từ khoá hay prompt riêng của một nghiệp vụ nào.

## User Scenarios & Testing *(mandatory)*

### Tác nhân

- **Người dùng cuối**: chat với tác tử qua một client (ứng dụng dùng chuẩn Claude Messages, hoặc widget web dùng chuẩn AG-UI).
- **Nhà tích hợp**: lập trình viên nối ứng dụng có sẵn vào BePilot.
- **Người vận hành**: cấu hình nhà cung cấp mô hình, quản lý skill và MCP server, theo dõi các lượt chạy.
- **Tác giả skill**: viết tài liệu skill cho một nghiệp vụ.

### User Story 1 - Trò chuyện với tác tử qua API tương thích Claude (Priority: P1)

Nhà tích hợp trỏ một client Claude có sẵn về BePilot, đổi URL và khoá API, rồi chat như bình thường. Tác tử trả lời dạng stream hoặc trả một lần, có thể gọi tool giữa chừng và báo tiến độ từng tool.

**Why this priority**: Đây là giá trị cốt lõi. Không có nó thì mọi tính năng khác vô nghĩa.

**Independent Test**: Gửi một câu hỏi bằng client Claude chuẩn tới BePilot đang chạy với nhà cung cấp giả lập (không cần mạng), rồi nhận được câu trả lời hợp lệ cả ở chế độ stream lẫn chế độ trả một lần.

**Acceptance Scenarios**:

1. **Given** một khoá API hợp lệ, **When** client gửi tin nhắn không kèm mã phiên, **Then** hệ thống tạo phiên mới (đặt tiêu đề theo tin nhắn đầu), trả lời và trả về mã phiên để dùng cho lượt sau.
2. **Given** yêu cầu bật stream, **When** tác tử trả lời, **Then** client nhận chuỗi sự kiện đúng thứ tự của chuẩn Claude Messages (bắt đầu tin nhắn → các khối nội dung → kết thúc), gồm cả văn bản, lời gọi tool và phần suy nghĩ, kèm sự kiện mở rộng báo tool bắt đầu và kết thúc chạy.
3. **Given** một mã phiên của chính người gọi, **When** client gửi tin nhắn tiếp theo kèm mã đó, **Then** tác tử trả lời có xét lịch sử hội thoại của phiên.
4. **Given** một mã phiên của người khác, **When** client gửi tin nhắn kèm mã đó, **Then** hệ thống từ chối.
5. **Given** khoá API sai hoặc thiếu, **When** client gọi bất kỳ API nghiệp vụ nào, **Then** hệ thống trả lỗi chưa xác thực.

---

### User Story 2 - Widget web chat qua chuẩn AG-UI (Priority: P1)

Một widget web xây trên chuẩn AG-UI chat với cùng tác tử, dùng `threadId` làm mã phiên. Widget có thể khai báo tool chạy phía client và phát lại lịch sử khi người dùng mở lại một luồng chat.

**Why this priority**: Đây là đường giao tiếp chính của các giao diện web, ví dụ widget thẩm định.

**Independent Test**: Gửi một yêu cầu AG-UI không có `threadId`, nhận về luồng sự kiện có `threadId` mới. Gửi lượt hai với `threadId` đó và thấy tác tử nhớ ngữ cảnh.

**Acceptance Scenarios**:

1. **Given** không có `threadId`, **When** widget gửi tin nhắn, **Then** hệ thống mở phiên mới và trả mã phiên ngay trong sự kiện bắt đầu lượt chạy.
2. **Given** một `threadId` của người khác, **When** widget gửi tin nhắn, **Then** hệ thống từ chối. Với `threadId` không tồn tại, hệ thống mở phiên mới.
3. **Given** widget khai báo một tool chạy phía client, **When** tác tử quyết định gọi tool đó, **Then** hệ thống phát sự kiện gọi tool rồi kết thúc lượt. Kết quả do client gửi lại ở lượt sau.
4. **Given** một yêu cầu chỉ có `threadId` hợp lệ mà không có tin nhắn người dùng mới, **When** widget gửi, **Then** hệ thống trả ảnh chụp toàn bộ tin nhắn đã lưu của phiên, không chạy lượt mới và không lưu gì thêm. Yêu cầu như vậy với `threadId` rỗng hoặc không tồn tại bị từ chối là yêu cầu sai.
5. **Given** `state` và `context` do widget gửi, **When** lượt chạy bắt đầu, **Then** chúng được đưa vào chỉ dẫn hệ thống của riêng lượt đó.

---

### User Story 3 - Quản lý phiên và xem lại hội thoại (Priority: P1)

Người dùng xem danh sách các cuộc chat của mình, mở lại toàn bộ hội thoại (kể cả lời gọi và kết quả tool), đổi tên, xoá, và xem báo cáo kết quả các tác vụ mà skill đã lưu.

**Why this priority**: Không có phiên thì không có hội thoại nhiều lượt và client không dựng được lịch sử chat.

**Independent Test**: Tạo hai phiên, liệt kê, mở một phiên, đổi tiêu đề, xoá phiên còn lại. Kiểm tra danh sách phản ánh đúng các thay đổi và người dùng khác không thấy các phiên này.

**Acceptance Scenarios**:

1. **Given** người dùng có nhiều phiên, **When** liệt kê phiên, **Then** kết quả xếp mới nhất trước, phân trang theo con trỏ và chỉ gồm phiên của người đó.
2. **Given** một phiên đã có nhiều lượt, **When** mở phiên, **Then** hệ thống trả bản hội thoại dựng lại theo dạng khối nội dung của chuẩn Claude, gồm cả lời gọi và kết quả tool.
3. **Given** một phiên, **When** liệt kê tin nhắn sau một số thứ tự cho trước, **Then** chỉ các tin nhắn sau mốc đó được trả về, đúng thứ tự.
4. **Given** một phiên, **When** cập nhật tiêu đề hoặc metadata, **Then** lần đọc sau thấy giá trị mới.
5. **Given** một phiên, **When** xoá, **Then** phiên biến mất khỏi danh sách và không truy cập được nữa (xoá mềm, dữ liệu vẫn được giữ).
6. **Given** skill đã lưu kết quả có cấu trúc cho các tác vụ trong phiên, **When** xem báo cáo phiên, **Then** hệ thống trả các kết quả đó theo khoá tác vụ. Báo cáo vẫn xem được kể cả khi phần chạy tác tử đang lỗi.

---

### User Story 4 - Tác tử tự tìm và dùng skill phù hợp (Priority: P2)

Tác giả skill đặt một tài liệu skill vào thư mục skill và đồng bộ. Từ đó, khi người dùng hỏi một việc liên quan, tác tử tự nhận ra skill phù hợp, nạp hướng dẫn và dùng đúng các tool skill cho phép, dù người dùng không hề nhắc tên skill.

**Why this priority**: Đây là cơ chế để lõi dùng chung mà vẫn làm được nghiệp vụ cụ thể.

**Independent Test**: Đồng bộ skill "PDF Forms", hỏi "giúp tôi điền form PDF với thông tin liên hệ" và thấy tác tử tự nạp skill đó.

**Acceptance Scenarios**:

1. **Given** một skill mới hoặc đã sửa trong thư mục skill, **When** chạy đồng bộ, **Then** skill được thêm hoặc cập nhật (chỉ tính lại vector biểu diễn khi nội dung đổi), và skill đã xoá khỏi thư mục bị gỡ khỏi hệ thống.
2. **Given** các skill đã đồng bộ, **When** người dùng gửi tin nhắn, **Then** các skill liên quan nhất theo ngữ nghĩa (dựa trên tin nhắn cuối và bản tóm tắt hội thoại) được liệt kê trong chỉ dẫn của lượt đó.
3. **Given** một skill được chọn có danh sách tool cho phép, **When** lượt chạy bắt đầu, **Then** các tool đó tự động sẵn sàng cho tác tử.
4. **Given** người vận hành, **When** liệt kê skill, **Then** thấy tên, mô tả và trạng thái đồng bộ của từng skill.

---

### User Story 5 - Gắn công cụ ngoài (MCP) không cần khởi động lại (Priority: P2)

Người vận hành gắn một MCP server qua file cấu hình hoặc qua API. Ngay từ tin nhắn kế tiếp, tác tử tìm và dùng được tool của server đó. Số lượng tool lớn không làm phình ngữ cảnh.

**Why this priority**: Mở rộng năng lực tác tử mà không phải sửa lõi hay dừng dịch vụ.

**Independent Test**: Khi server đang chạy, thêm một MCP server qua API, gửi một tin nhắn cần tool của nó và thấy tác tử tìm rồi gọi được tool đó, không khởi động lại.

**Acceptance Scenarios**:

1. **Given** MCP đang bật, **When** thêm server qua API, **Then** hệ thống kết nối ngay trước khi trả lời. Kết nối được thì lưu lại để tự gắn lại sau khi khởi động lại. Không kết nối được thì báo lỗi và không lưu gì.
2. **Given** file cấu hình MCP bị sửa khi đang chạy, **When** hết chu kỳ đọc lại, **Then** server mới được gắn, server bị xoá hoặc tắt được gỡ, server bị đổi cấu hình được kết nối lại.
3. **Given** một tên server đã thuộc về file cấu hình, **When** thêm hoặc xoá server cùng tên qua API, **Then** hệ thống báo xung đột.
4. **Given** các tool ngoài đã gắn, **When** lượt chạy bắt đầu, **Then** chỉ tên của chúng xuất hiện trong chỉ dẫn. Tác tử phải tìm tool (theo tên chính xác hoặc từ khoá) để nạp mô tả đầy đủ trước khi gọi. Tool đã nạp giữ nguyên cho các lượt sau của cùng phiên.
5. **Given** tool từ nhiều server, **When** đăng ký, **Then** mỗi tool mang tên có tiền tố theo server nên không bao giờ trùng nhau.
6. **Given** không có tool ngoài nào, **When** lượt chạy bắt đầu, **Then** công cụ tìm tool không được cung cấp và hành vi giống như khi MCP tắt.
7. **Given** MCP đang tắt, **When** gọi API quản lý MCP, **Then** hệ thống báo dịch vụ không khả dụng.

---

### User Story 6 - Nhắn thêm hoặc dừng khi tác tử đang làm (Priority: P2)

Khi tác tử đang chạy một lượt dài, người dùng nhắn thêm ý ("dùng số liệu năm 2025 nhé") mà không phải chờ. Tác tử đọc ý đó trước bước kế tiếp. Người dùng cũng có thể nhắn "dừng" để ngắt lượt đang chạy.

**Why this priority**: Giữ trải nghiệm chat tự nhiên với các tác vụ chạy lâu.

**Independent Test**: Bắt đầu một lượt có nhiều bước tool, gửi tin nhắn thứ hai giữa chừng, rồi kiểm tra yêu cầu thứ hai được chấp nhận ngay và câu trả lời cuối có xét nội dung tin nhắn đó.

**Acceptance Scenarios**:

1. **Given** một lượt đang chạy trên phiên, **When** người dùng gửi tin nhắn mới, **Then** tin nhắn được lưu ngay, yêu cầu được trả lời ngay với trạng thái "đã chuyển cho lượt đang chạy", và lượt đó đọc tin nhắn trước lần gọi mô hình kế tiếp. Câu trả lời xuất hiện trên luồng của lượt đang chạy.
2. **Given** một lượt đang chạy, **When** người dùng gửi tin nhắn ngắn (tối đa 3 từ) bắt đầu bằng từ dừng ("stop", "cancel", "abort", "halt", "dừng", "hủy"/"huỷ", kể cả dạng không dấu), **Then** lượt bị ngắt, giữ lại phần đã làm, kết thúc với lý do "bị ngắt", rồi tin nhắn đó được xử lý như một lượt bình thường.
3. **Given** một tin nhắn dài chỉ tình cờ bắt đầu bằng từ dừng ("hủy hợp đồng số 3 giúp tôi"), **When** gửi lúc đang chạy, **Then** hệ thống coi đó là tin nhắn thêm ý, không phải lệnh dừng.
4. **Given** một tin nhắn đến quá muộn để lượt hiện tại kịp đọc, **When** lượt kết thúc, **Then** hệ thống chạy một lượt tiếp theo để trả lời tin nhắn đó, không tin nhắn nào bị bỏ sót.
5. **Given** nhiều yêu cầu ghi đồng thời vào cùng một phiên, **When** chúng được lưu, **Then** thứ tự tin nhắn trong phiên không bao giờ trùng hoặc lẫn.

---

### User Story 7 - Câu trả lời luôn hoàn chỉnh và chính xác (Priority: P2)

Người dùng luôn nhận câu trả lời trọn vẹn: không bị cắt giữa câu hay giữa JSON, phép tính chính xác, và JSON trong câu trả lời luôn hợp lệ.

**Why this priority**: Các nghiệp vụ như thẩm định dùng số liệu và JSON có cấu trúc. Sai một con số hay JSON hỏng là kết quả không dùng được.

**Independent Test**: Yêu cầu một tài liệu dài vượt giới hạn đầu ra một lần, và một JSON chứa các giá trị phải tính toán. Kiểm tra tài liệu liền mạch đến hết, và JSON hợp lệ với giá trị đã tính đúng.

**Acceptance Scenarios**:

1. **Given** câu trả lời bị cắt vì chạm giới hạn đầu ra, **When** điều đó xảy ra, **Then** hệ thống tự viết tiếp (tối đa 4 lần cho mỗi lần gọi mô hình) và người dùng thấy một câu trả lời liền mạch. Lời gọi tool bị cắt thì không viết tiếp.
2. **Given** một lượt có ngân sách số lần gọi mô hình, **When** còn 3 lần gọi, **Then** tác tử được báo trước. Lần gọi cuối không có tool và bắt buộc phải trả lời, nên lượt luôn kết thúc bằng một câu trả lời hoàn chỉnh chứ không phải lỗi.
3. **Given** cần tính toán, **When** tác tử trả lời, **Then** tác tử được hướng dẫn dùng công cụ tính chính xác (số hữu tỉ, không sai số dấu phẩy động, nhiều biểu thức trong một lần gọi) thay vì tự nhẩm.
4. **Given** câu trả lời chứa JSON (khối code JSON hoặc object/array đứng riêng), **When** stream tới client, **Then** phần JSON được giữ lại tới khi đủ, mọi biểu thức số học còn sót ở vị trí giá trị được tính chính xác trước khi gửi và lưu, và mỗi lần sửa được ghi cảnh báo trong log. Văn xuôi và các khối code khác giữ nguyên.
5. **Given** một chuỗi JSON không hợp lệ, **When** tác tử dùng công cụ kiểm tra JSON, **Then** công cụ báo dòng, cột và gợi ý sửa.

---

### User Story 8 - Chọn nhà cung cấp mô hình, kể cả chạy hoàn toàn offline (Priority: P3)

Người vận hành chọn nhà cung cấp mặc định (Claude, máy chủ kiểu OpenAI kể cả máy chủ local, Volcengine Ark, hoặc bộ giả lập). Nhà tích hợp có thể chọn nhà cung cấp cho từng yêu cầu. Lập trình viên chạy và kiểm thử được toàn bộ hệ thống mà không cần khoá API.

**Why this priority**: Linh hoạt chi phí và hạ tầng, và kiểm thử được mà không phụ thuộc bên ngoài.

**Independent Test**: Khởi động với bộ giả lập, không có mạng ra ngoài, rồi chạy toàn bộ luồng chat, phiên và skill thành công.

**Acceptance Scenarios**:

1. **Given** nhà cung cấp mặc định đã cấu hình, **When** yêu cầu không chỉ định nhà cung cấp, **Then** dùng nhà cung cấp mặc định. Yêu cầu có chỉ định thì dùng nhà cung cấp đó.
2. **Given** nhà cung cấp gặp lỗi tạm thời, **When** gọi mô hình, **Then** hệ thống tự thử lại trước khi báo lỗi.
3. **Given** bộ giả lập, **When** gửi cùng một yêu cầu nhiều lần, **Then** kết quả lặp lại y hệt.
4. **Given** đã cấu hình nhiệt độ mặc định, **When** yêu cầu không nêu nhiệt độ, **Then** dùng giá trị mặc định đó.

---

### User Story 9 - Không mất dữ liệu và theo dõi được từng lượt (Priority: P3)

Người vận hành tin rằng không lượt chat nào bị mất kể cả khi client rớt kết nối hay mô hình lỗi, và xem lại được mỗi lượt đã chạy thế nào để so sánh hoặc gỡ lỗi.

**Why this priority**: Cần cho vận hành thật, kiểm toán và cải tiến chất lượng.

**Independent Test**: Ngắt kết nối client giữa một lượt stream, rồi kiểm tra phiên vẫn có tin nhắn người dùng và phần trả lời dở, và có bản ghi lượt chạy với lý do kết thúc.

**Acceptance Scenarios**:

1. **Given** một tin nhắn người dùng, **When** hệ thống nhận được, **Then** nó được lưu ngay, trước khi gọi mô hình.
2. **Given** lượt bị lỗi hoặc client ngắt kết nối giữa chừng, **When** lượt kết thúc, **Then** phần trả lời đã sinh ra vẫn được lưu.
3. **Given** mỗi lượt chạy, **When** lượt kết thúc, **Then** có đúng một bản ghi lượt chạy gồm nhà cung cấp, mô hình, số bước, số token, độ trễ, lỗi (nếu có), lý do kết thúc, giới hạn đầu ra, nhiệt độ, số lần gọi theo từng tool, skill đã dùng, tin nhắn chen ngang và việc bị ngắt.
4. **Given** hội thoại dài, **When** dựng ngữ cảnh cho lượt mới, **Then** lịch sử được cắt theo ngân sách token. Một tác vụ nền định kỳ (mặc định 8 lượt một lần) làm mới bản tóm tắt hội thoại, và bản tóm tắt được đưa vào chỉ dẫn.
5. **Given** bất kỳ yêu cầu nào, **When** hệ thống ghi log, **Then** log có cấu trúc và mang mã yêu cầu để lần theo.

---

### User Story 10 - Tài liệu API và kiểm tra sức khoẻ (Priority: P3)

Nhà tích hợp mở trang tài liệu API tương tác, dùng được kể cả khi không có mạng. Hạ tầng triển khai kiểm tra được dịch vụ còn sống và đã sẵn sàng.

**Why this priority**: Giảm công tích hợp và cần cho triển khai.

**Independent Test**: Mở trang tài liệu và tải bản đặc tả API khi không có internet, rồi gọi hai điểm kiểm tra sức khoẻ.

**Acceptance Scenarios**:

1. **Given** dịch vụ đang chạy, **When** mở trang tài liệu, **Then** thấy giao diện tài liệu tương tác. Trang và bản đặc tả thô xem được mà không cần xác thực hay internet.
2. **Given** mọi API đã công bố, **When** chạy kiểm thử, **Then** kiểm thử thất bại nếu có API thiếu tài liệu. Tài liệu luôn được sinh từ mã nguồn, không viết tay.
3. **Given** cơ sở dữ liệu không truy cập được, **When** gọi điểm kiểm tra sẵn sàng, **Then** dịch vụ báo chưa sẵn sàng, trong khi điểm kiểm tra còn sống vẫn báo bình thường.

---

### Edge Cases

- Tin nhắn chỉ có khoảng trắng hoặc không có tin nhắn người dùng: Messages API từ chối, AG-UI coi đó là yêu cầu phát lại lịch sử (xem US2-4).
- Ngân sách bước bị vượt khi tác tử còn muốn gọi tool: lần gọi cuối không có tool, buộc phải trả lời.
- Mô hình trả lời bị cắt ngay trong lời gọi tool: không viết tiếp, vì đối số của lời gọi bị cắt không dùng được.
- JSON trong câu trả lời không bao giờ đóng lại khi khối văn bản kết thúc: phần đã giữ lại vẫn được gửi ra, không mất nội dung.
- MCP server chết giữa chừng: tool của nó báo lỗi khi được gọi. Server khai báo trong file mà chưa gắn được sẽ được thử lại mỗi chu kỳ đọc file.
- Đổi bộ tạo vector biểu diễn có số chiều khác: phải đồng bộ lại skill. Số chiều khác với cấu hình lưu trữ cần thiết lập lại kho dữ liệu.
- Tool lưu kết quả tác vụ được gọi khi chưa cấu hình kho lưu: tool báo rõ là chưa cấu hình, không làm hỏng lượt.
- Chế độ bỏ qua xác thực (chỉ cho phát triển local): mọi yêu cầu được coi là của một người dùng dev cố định.

## Requirements *(mandatory)*

### Functional Requirements

**Giao tiếp hội thoại**

- **FR-001**: Hệ thống MUST cung cấp điểm chat tương thích chuẩn Claude Messages (mô hình, giới hạn đầu ra, danh sách tin nhắn dạng chuỗi hoặc mảng khối, chỉ dẫn hệ thống, nhiệt độ, stream), để client Claude có sẵn dùng được mà chỉ đổi địa chỉ và khoá.
- **FR-002**: Hệ thống MUST hỗ trợ cả trả lời một lần và trả lời stream. Stream MUST phát đúng loại và thứ tự sự kiện của chuẩn Claude (văn bản, đối số tool, suy nghĩ, ping, lỗi), cộng thêm sự kiện mở rộng báo tool bắt đầu và kết thúc chạy.
- **FR-003**: Hệ thống MUST chấp nhận hai phần mở rộng: chọn nhà cung cấp cho từng yêu cầu, và mã phiên trong metadata. Mã phiên MUST có trong phản hồi (cả stream lẫn một lần).
- **FR-004**: Hệ thống MUST cung cấp điểm chat theo chuẩn AG-UI. Điểm này luôn trả luồng sự kiện AG-UI (bắt đầu lượt, văn bản, lời gọi tool, kết quả tool, kết thúc hoặc lỗi), không chuyển tiếp phần suy nghĩ, và coi `threadId` là mã phiên.
- **FR-005**: Trên AG-UI, chỉ tin nhắn người dùng cuối cùng khởi động lượt chạy. Lịch sử MUST được nạp từ phía máy chủ. `state` và `context` MUST được đưa vào chỉ dẫn của lượt. Tool khai báo phía client MUST được trao cho tác tử như tool chạy phía client.
- **FR-006**: Hai điểm chat MUST dùng chung một tác tử, một cơ chế xác thực và một mô hình sự kiện nội bộ. Chỉ định dạng truyền khác nhau.

**Phiên & quyền sở hữu**

- **FR-007**: Mọi API nghiệp vụ MUST yêu cầu khoá API (qua header khoá riêng hoặc Bearer). Chỉ tài liệu API và điểm kiểm tra sức khoẻ là công khai.
- **FR-008**: Người dùng MUST chỉ truy cập được phiên của chính mình. Truy cập phiên của người khác MUST bị từ chối.
- **FR-009**: Người dùng MUST tạo được phiên (tuỳ chọn tiêu đề, nhà cung cấp, mô hình, chỉ dẫn riêng, metadata), liệt kê phiên (mới nhất trước, phân trang theo con trỏ), xem phiên kèm bản hội thoại dựng lại, liệt kê tin nhắn sau một số thứ tự, sửa tiêu đề hoặc metadata, và xoá mềm phiên.
- **FR-010**: Tác tử MUST lưu được kết quả có cấu trúc của từng tác vụ theo khoá tác vụ trong phiên (gọi lại cùng khoá thì thay bản cũ). Người dùng MUST xem được báo cáo các kết quả đó mà không phụ thuộc vào việc tác tử có đang chạy được hay không.

**Chỉ dẫn & skill**

- **FR-011**: Chỉ dẫn hệ thống MUST được dựng lại ở mỗi lượt từ các phần theo thứ tự cố định: danh tính, môi trường hiện tại (ngày giờ, mô hình, phiên, tool, ngân sách bước), context và state do client gửi, tóm tắt hội thoại, hướng dẫn dùng tool, quy tắc chính xác, danh sách tên tool hoãn, danh mục skill liên quan, phong cách trả lời, và chỉ dẫn riêng của phiên. Phần nào không có dữ liệu thì bỏ qua.
- **FR-012**: Skill MUST là tài liệu trong thư mục skill (tên, mô tả, danh sách tool cho phép, nội dung hướng dẫn). Thư mục là nguồn gốc. Đồng bộ MUST thêm hoặc cập nhật skill thay đổi, chỉ tính lại vector biểu diễn khi nội dung đổi, và gỡ skill đã xoá. Đồng bộ chạy được bằng lệnh hoặc qua API.
- **FR-013**: Ở mỗi lượt, hệ thống MUST chọn các skill liên quan theo độ tương đồng ngữ nghĩa với tin nhắn cuối và bản tóm tắt, rồi liệt kê chúng trong chỉ dẫn mà người dùng không cần nêu tên. Tác tử MUST nạp được toàn văn skill khi cần.
- **FR-014**: Tool mà một skill được chọn cho phép MUST tự động sẵn sàng trong lượt đó.

**Tool**

- **FR-015**: Các tool dựng sẵn MUST luôn có: thời gian hiện tại, tính toán chính xác, kiểm tra JSON, tải nội dung HTTP, tìm kiếm web, nạp skill, và lưu kết quả tác vụ. Công cụ tìm tool chỉ có khi có tool ngoài.
- **FR-016**: Tool ngoài MUST ở dạng hoãn: chỉ tên xuất hiện trong chỉ dẫn, tác tử phải tìm (theo tên chính xác, theo từ khoá, hoặc bắt buộc khớp tên) để nạp mô tả đầy đủ trước khi gọi. Tool đã nạp MUST được giữ cho các lượt sau của cùng phiên.
- **FR-017**: Công cụ tính toán MUST cho kết quả chính xác (không sai số dấu phẩy động) và nhận nhiều biểu thức trong một lần gọi.
- **FR-018**: Công cụ kiểm tra JSON MUST báo vị trí lỗi (dòng, cột) và gợi ý sửa.

**MCP**

- **FR-019**: Người vận hành MUST bật hoặc tắt được hệ MCP. Khi tắt, API MCP báo không khả dụng.
- **FR-020**: MCP server MUST khai báo được qua file cấu hình (đọc lại định kỳ khi đang chạy, hỗ trợ biến môi trường, danh sách tool được phép, tắt tạm từng server) hoặc qua API (lưu bền, tự gắn lại khi khởi động). Hỗ trợ ba kiểu kết nối: tiến trình con, SSE và HTTP streamable.
- **FR-021**: Thêm hoặc gỡ server MUST có hiệu lực từ tin nhắn kế tiếp mà không cần khởi động lại.
- **FR-022**: Tool của MCP server MUST mang tên có tiền tố theo server, không bao giờ trùng giữa các server.
- **FR-023**: API thêm server MUST kết nối thử trước khi trả lời. Nếu lỗi kết nối thì báo lỗi cổng và không lưu. Tên đã thuộc file cấu hình thì báo xung đột. Chỉ server đăng ký qua API mới gỡ được qua API.
- **FR-024**: Kho dữ liệu chỉ được đọc khi khởi động và khi ghi qua API, không đọc trên đường xử lý tin nhắn.

**Lượt chạy, nhắn chen & dừng**

- **FR-025**: Mỗi phiên MUST chỉ chạy một lượt tại một thời điểm. Tin nhắn đến khi đang chạy MUST được lưu ngay, phản hồi ngay (Messages API: trạng thái chấp nhận kèm loại "steered"; AG-UI: một cặp bắt đầu và kết thúc rỗng), và được lượt đang chạy đọc trước lần gọi mô hình kế tiếp.
- **FR-026**: Tin nhắn tối đa 3 từ, bắt đầu bằng một từ dừng (stop, cancel, abort, halt, dừng, hủy/huỷ, kể cả không dấu) MUST ngắt lượt đang chạy, giữ phần đã làm, kết thúc với lý do "bị ngắt", rồi được xử lý như lượt thường. Tin nhắn dài hơn MUST không bị coi là lệnh dừng.
- **FR-027**: Tin nhắn đến quá muộn để lượt đang chạy kịp đọc MUST được trả lời bằng một lượt tiếp theo.
- **FR-028**: Việc ghi vào một phiên MUST được tuần tự hoá để số thứ tự tin nhắn không bao giờ trùng.
- **FR-029**: Câu trả lời bị cắt do giới hạn đầu ra MUST được viết tiếp tự động (tối đa 4 lần cho mỗi lần gọi mô hình) và stream như một câu trả lời liền mạch. Lời gọi tool bị cắt MUST không được viết tiếp.
- **FR-030**: Mỗi lượt MUST có ngân sách số lần gọi mô hình, được nêu trong chỉ dẫn. Tác tử MUST được báo khi còn 3 lần. Lần cuối MUST không có tool, để lượt luôn kết thúc bằng câu trả lời.
- **FR-031**: JSON trong câu trả lời (khối code JSON hoặc object/array đứng riêng) MUST được giữ lại tới khi đủ. Mọi biểu thức số học ở vị trí giá trị MUST được tính chính xác trước khi stream và lưu, và mỗi lần sửa MUST được ghi cảnh báo. Văn xuôi và các khối code khác MUST giữ nguyên.

**Nhà cung cấp mô hình**

- **FR-032**: Hệ thống MUST hỗ trợ các nhà cung cấp Claude, máy chủ kiểu OpenAI (kể cả máy chủ local không cần khoá, chấp nhận cả địa chỉ gốc lẫn địa chỉ endpoint đầy đủ), Volcengine Ark, và bộ giả lập tất định để phát triển offline.
- **FR-033**: Nhà cung cấp và mô hình MUST chọn được theo mặc định cấu hình hoặc theo từng yêu cầu. Nhiệt độ mặc định MUST áp dụng khi yêu cầu không nêu.
- **FR-034**: Lỗi tạm thời từ nhà cung cấp MUST được tự động thử lại trước khi báo lỗi.

**Lưu trữ, ngữ cảnh & quan sát**

- **FR-035**: Tin nhắn người dùng MUST được lưu ngay khi nhận. Tin nhắn trả lời, kể cả phần dở khi lỗi hoặc client ngắt kết nối, MUST luôn được lưu, không phụ thuộc kết nối của client.
- **FR-036**: Lịch sử MUST được chuyển thành bản hội thoại hợp lệ và cắt theo ngân sách token. Bản tóm tắt hội thoại MUST được làm mới định kỳ ở chế độ nền.
- **FR-037**: Mỗi lượt MUST tạo đúng một bản ghi lượt chạy với các trường nêu ở US9-3.
- **FR-038**: Log MUST có cấu trúc và mang mã yêu cầu.

**Tài liệu & vận hành**

- **FR-039**: Tài liệu API tương tác và bản đặc tả thô MUST dùng được không cần xác thực và không cần internet. Bản đặc tả MUST được sinh từ chú thích trong mã nguồn. Kiểm thử MUST thất bại nếu có API thiếu chú thích.
- **FR-040**: Hệ thống MUST có điểm kiểm tra còn sống và điểm kiểm tra sẵn sàng. Điểm sẵn sàng kiểm tra cả kết nối kho dữ liệu.
- **FR-041**: Hệ thống MUST có công cụ tạo người dùng kèm khoá API, công cụ đồng bộ skill, và chế độ chỉ chạy migration.
- **FR-042**: Lõi tác tử MUST không chứa logic, từ khoá hay prompt riêng của bất kỳ nghiệp vụ nào. Mọi nghiệp vụ đi qua skill và tool.

### Key Entities

- **Người dùng (User)**: chủ sở hữu phiên, có một hoặc nhiều khoá API.
- **Khoá API (API Key)**: thông tin xác thực gắn với một người dùng.
- **Phiên (Session)**: một cuộc hội thoại. Có tiêu đề, nhà cung cấp và mô hình tuỳ chọn, chỉ dẫn riêng, metadata, bản tóm tắt hội thoại, cờ xoá mềm. Thuộc một người dùng.
- **Tin nhắn (Message)**: một lượt nói trong phiên (người dùng, trợ lý hoặc kết quả tool) với số thứ tự tăng dần duy nhất trong phiên. Nội dung dạng khối: văn bản, lời gọi tool, kết quả tool, suy nghĩ.
- **Lượt chạy (Agent Run)**: một lần tác tử xử lý một lượt. Chứa nhà cung cấp, mô hình, số bước, token, độ trễ, lỗi và chi tiết hành vi (lý do kết thúc, tool, skill, tin nhắn chen ngang, bị ngắt).
- **Kết quả tác vụ (Task Result)**: kết quả có cấu trúc do skill lưu, theo cặp phiên và khoá tác vụ, có tiêu đề. Dùng để dựng báo cáo phiên.
- **Skill**: tài liệu hướng dẫn nghiệp vụ với tên, mô tả (dùng để truy hồi), danh sách tool cho phép, nội dung, dấu kiểm nội dung và vector biểu diễn.
- **Tool**: năng lực tác tử gọi được. Có ba loại: dựng sẵn (luôn có), ngoài (hoãn, từ MCP) và phía client (do widget AG-UI khai báo).
- **MCP Server**: nguồn tool ngoài. Có tên, kiểu kết nối, thông số kết nối, danh sách tool được phép, nguồn khai báo (file hoặc API) và trạng thái gắn.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: Một client Claude chuẩn nối được vào BePilot và hoàn tất hội thoại nhiều lượt chỉ bằng cách đổi địa chỉ máy chủ và khoá, không sửa mã client.
- **SC-002**: 100% lượt chạy kết thúc bằng câu trả lời hoàn chỉnh hoặc lý do kết thúc rõ ràng (hoàn tất, bị ngắt, lỗi). Không lượt nào kết thúc im lặng hay dừng giữa câu vì giới hạn đầu ra.
- **SC-003**: 0 tin nhắn người dùng bị mất, kể cả khi client ngắt kết nối, mô hình lỗi, hay tin nhắn được gửi trong lúc lượt đang chạy.
- **SC-004**: Tin nhắn gửi trong lúc tác tử đang chạy được chấp nhận ngay lập tức (người dùng không phải chờ lượt cũ xong). Lệnh dừng ngắt lượt trước lần gọi mô hình kế tiếp.
- **SC-005**: Một MCP server mới có tool dùng được ngay từ tin nhắn kế tiếp, không có thời gian dừng dịch vụ.
- **SC-006**: Một skill mới có hiệu lực sau một lần đồng bộ, và được tác tử tự chọn cho câu hỏi liên quan mà người dùng không cần nêu tên skill.
- **SC-007**: 100% JSON hệ thống gửi ra trong câu trả lời không chứa biểu thức số học chưa tính. Mọi phép tính qua công cụ tính toán cho kết quả chính xác tuyệt đối.
- **SC-008**: Lập trình viên mới dựng được môi trường và chạy hội thoại đầu tiên mà không cần bất kỳ khoá API bên ngoài nào. Toàn bộ kiểm thử đơn vị chạy được không cần mạng.
- **SC-009**: 100% lượt chạy có đúng một bản ghi lượt chạy đủ thông tin để so sánh hai lần chạy của cùng một yêu cầu.
- **SC-010**: 100% API đã công bố có trong tài liệu sinh tự động.
- **SC-011**: Thêm một nghiệp vụ mới không cần sửa mã lõi tác tử, chỉ cần thêm skill và tool.

## Assumptions

- Spec phản ánh hành vi tại commit `cc47ba5` (2026-09-23). Thư viện `docpipe/` (chưa commit) và widget thẩm định trong `frontend/` nằm ngoài phạm vi và sẽ có spec riêng.
- Giá trị mặc định hiện tại: ngân sách 16 lần gọi mô hình mỗi lượt, ngân sách lịch sử 24.000 token, làm mới tóm tắt mỗi 8 lượt, MCP tắt, chu kỳ đọc lại file MCP 10 giây, thời gian chờ kết nối MCP 20 giây. Đây là tham số cấu hình, không phải yêu cầu cứng.
- Truy hồi skill dùng vector biểu diễn với số chiều cố định theo cấu hình lưu trữ. Có bộ tạo vector offline (băm) cho phát triển và bộ tạo vector thật (kiểu OpenAI) cho vận hành.
- Chế độ bỏ qua xác thực chỉ dùng cho phát triển local, không bao giờ bật trên môi trường triển khai.
- Xác thực chỉ dùng khoá API, chưa có phân quyền theo vai trò hay chia sẻ phiên giữa người dùng.
- Các trường `forwardedProps` của AG-UI được chấp nhận nhưng chưa có tác dụng.
