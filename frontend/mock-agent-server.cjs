// Minimal AG-UI-compliant mock server, for local dev/testing only.
// Point the widget's data-agent-url (or mount() `agentUrl`) at a real
// backend for production — this just proves the wire protocol end to end.
const http = require("node:http");

function corsHeaders() {
  return {
    "Access-Control-Allow-Origin": "*",
    "Access-Control-Allow-Methods": "POST, OPTIONS",
    "Access-Control-Allow-Headers": "Content-Type, Authorization",
  };
}

function sse(res, event) {
  res.write(`data: ${JSON.stringify(event)}\n\n`);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function chunkText(text, size = 3) {
  const chunks = [];
  for (let i = 0; i < text.length; i += size) chunks.push(text.slice(i, i + size));
  return chunks;
}

function craftReply(userText) {
  const s = (userText || "").toLowerCase();

  if (s.startsWith("giao việc:") || s.includes("giao thêm") || s === "mình muốn giao thêm đầu việc") {
    return {
      reply:
        "Em đã nhận đầu việc chị vừa giao và sẽ cập nhật kết quả vào tờ trình trong ít phút nữa.",
    };
  }
  if (s.includes("tờ trình") || s.includes("to trinh")) {
    return {
      reply:
        "Em đã chèn tờ trình nháp v0.3 vào hồ sơ, kèm cấu trúc khoản vay đề xuất bên dưới.",
      tool: {
        name: "chen_to_trinh_nhap",
        args: { hanMuc: "22,0 tỷ", thoiHan: "12 tháng", laiSuat: "Sàn + 3,2%" },
      },
    };
  }
  if (s.includes("dscr")) {
    return {
      reply:
        "Kịch bản cơ sở DSCR ước tính 1,25 lần. Ở kịch bản xấu (biên lợi nhuận gộp giảm 2 điểm %), DSCR giảm còn khoảng 1,08 — dưới ngưỡng tối thiểu 1,2 theo chính sách tín dụng.",
    };
  }
  if (s.includes("phải thu")) {
    return {
      reply:
        "Khoản phải thu khác tăng đột biến 6,1 tỷ trong Q4/2025, tập trung ở 2 đối tượng chưa được thuyết minh làm rõ. Em đề xuất yêu cầu bên vay bổ sung bảng kê chi tiết và biên bản đối chiếu công nợ.",
    };
  }
  if (s.includes("thẩm quyền") || s.includes("nghị quyết")) {
    return {
      reply:
        "Nghị quyết HĐQT hiện tại chỉ phê duyệt phương án vay 20 tỷ, thấp hơn mức đề nghị 25 tỷ. Em đã tạo yêu cầu bổ sung nghị quyết HĐQT trong hồ sơ LOS.",
    };
  }
  return {
    reply: `Em ghi nhận: "${userText}". Đây là phản hồi từ mock AG-UI server dùng để kiểm thử — trỏ data-agent-url sang backend thật khi triển khai.`,
  };
}

const server = http.createServer((req, res) => {
  if (req.method === "OPTIONS") {
    res.writeHead(204, corsHeaders());
    res.end();
    return;
  }
  if (req.method !== "POST") {
    res.writeHead(405, corsHeaders());
    res.end();
    return;
  }

  let body = "";
  req.on("data", (chunk) => (body += chunk));
  req.on("end", async () => {
    let input = {};
    try {
      input = JSON.parse(body || "{}");
    } catch {
      // ignore malformed bodies, fall back to defaults below
    }

    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      Connection: "keep-alive",
      ...corsHeaders(),
    });

    const threadId = input.threadId || "mock-thread";
    const runId = input.runId || `mock-run-${Date.now()}`;
    const messages = Array.isArray(input.messages) ? input.messages : [];
    const lastUser = [...messages].reverse().find((m) => m.role === "user");
    const userText = typeof lastUser?.content === "string" ? lastUser.content : "";

    sse(res, { type: "RUN_STARTED", threadId, runId });

    const { reply, tool } = craftReply(userText);
    const messageId = `msg-${Date.now()}`;

    sse(res, { type: "TEXT_MESSAGE_START", messageId, role: "assistant" });
    for (const chunk of chunkText(reply)) {
      sse(res, { type: "TEXT_MESSAGE_CONTENT", messageId, delta: chunk });
      await delay(18);
    }
    sse(res, { type: "TEXT_MESSAGE_END", messageId });

    if (tool) {
      const toolCallId = `tool-${Date.now()}`;
      sse(res, {
        type: "TOOL_CALL_START",
        toolCallId,
        toolCallName: tool.name,
        parentMessageId: messageId,
      });
      for (const chunk of chunkText(JSON.stringify(tool.args), 12)) {
        sse(res, { type: "TOOL_CALL_ARGS", toolCallId, delta: chunk });
        await delay(12);
      }
      sse(res, { type: "TOOL_CALL_END", toolCallId });
    }

    sse(res, { type: "RUN_FINISHED", threadId, runId });
    res.end();
  });
});

const port = process.env.PORT || 8787;
server.listen(port, () => {
  console.log(`Mock AG-UI agent server listening on http://localhost:${port}/agent`);
});
