const SNIPPET = `<script
  src="https://cdn.example.com/appraisal-agent-widget.js"
  data-loan-id="LOS-2026-04871"
  data-agent-url="https://agents.example.com/appraisal-agent"
  data-agent-auth-token="..."
  async
></script>`;

/**
 * Minimal stand-in for a host page (e.g. the real LOS system). It exists only
 * to prove the widget mounts cleanly next to arbitrary host content — it is
 * not a rebuild of the LOS UI itself.
 */
function App() {
  return (
    <main className="mx-auto max-w-xl px-6 py-16 font-sans text-slate-800">
      <p className="mb-1 text-xs font-semibold tracking-wide text-slate-400 uppercase">
        Host page placeholder
      </p>
      <h1 className="mb-3 text-xl font-semibold">
        Trang hệ thống LOS (giả lập)
      </h1>
      <p className="mb-6 text-sm leading-relaxed text-slate-600">
        Đây chỉ là một trang nền tối giản để xem thử widget chat agent được
        nhúng vào bên dưới — không phải bản dựng lại giao diện LOS thật. Trong
        thực tế, đoạn script bên dưới được chèn vào trang của hệ thống LOS
        hiện có; widget tự tạo container Shadow DOM riêng nên không phụ thuộc
        hay xung đột với CSS của trang chủ.
      </p>
      <pre className="overflow-x-auto rounded-lg border border-slate-200 bg-slate-50 p-4 text-[12.5px] text-slate-700">
        <code>{SNIPPET}</code>
      </pre>
    </main>
  );
}

export default App;
