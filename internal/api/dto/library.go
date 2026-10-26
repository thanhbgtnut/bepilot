package dto

import (
	"time"

	"github.com/thanhenti/bepilot/internal/domain"
)

// Document is an uploaded document and how far reading it has got.
type Document struct {
	ID        string `json:"id" example:"6f1c2b6e-0f6a-4b8e-9f0e-0d4d2b7f3a11"`
	Name      string `json:"name,omitempty" example:"bctc-q3-2025.pdf"`
	MediaType string `json:"media_type" example:"application/pdf"`
	SizeBytes int64  `json:"size_bytes" example:"482113"`
	// Status is pending (waiting for a worker), processing, ready or failed.
	Status      string `json:"status" example:"processing" enums:"pending,processing,ready,failed"`
	PagesTotal  int    `json:"pages_total" example:"50"` // 0 until the file is opened
	PagesDone   int    `json:"pages_done" example:"23"`  // pages read so far
	FailedPages int    `json:"failed_pages" example:"0"` // pages that could not be read (ready documents)
	TokenCount  int    `json:"token_count" example:"0"`  // approximate tokens of the full text (ready documents)
	// UnverifiedNumbers counts numbers the vision model and OCR read
	// differently and that were not confirmed; each is noted in its page.
	UnverifiedNumbers int        `json:"unverified_numbers" example:"0"`
	Error             string     `json:"error,omitempty"`
	Attempts          int        `json:"attempts" example:"1"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	FinishedAt        *time.Time `json:"finished_at,omitempty"`
}

// DocumentFromDomain converts a stored document.
func DocumentFromDomain(d domain.Document) Document {
	return Document{
		ID: d.ID.String(), Name: d.Name, MediaType: d.MediaType, SizeBytes: d.Size, Status: d.Status,
		PagesTotal: d.PagesTotal, PagesDone: d.PagesDone, FailedPages: d.FailedPages, TokenCount: d.TokenCount,
		UnverifiedNumbers: d.UnverifiedNumbers, Error: d.Error, Attempts: d.Attempts,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt, FinishedAt: d.FinishedAt,
	}
}

// UploadDocumentResponse is the body of POST /v1/documents.
type UploadDocumentResponse struct {
	Document
	// Duplicate is set when the user had already uploaded the same file: the
	// existing document is returned and not read again.
	Duplicate bool          `json:"duplicate"`
	Session   *SessionBrief `json:"session,omitempty"` // the session the document was attached to
}

// DocumentList is the paginated response of GET /v1/documents.
type DocumentList struct {
	Data       []Document `json:"data"`
	HasMore    bool       `json:"has_more"`
	NextCursor string     `json:"next_cursor,omitempty"`
}

// StoredPage is one page of an uploaded document as it was read.
type StoredPage struct {
	PageNo int `json:"page_no" example:"12"`
	// Markdown is the page text given to the agent. Numbers the vision
	// model and OCR disagree on, and that were not confirmed, are listed
	// in a trailing <!-- unverified numbers: … --> comment.
	Markdown string `json:"markdown"`
	OCRText  string `json:"ocr_text"`
	// Numbers is the number check of the page: conflicts found, the vision
	// model's answers and what stays unverified.
	Numbers    any    `json:"numbers,omitempty" swaggertype:"object"`
	Unverified int    `json:"unverified" example:"0"`
	Error      string `json:"error,omitempty"`
	Tokens     int    `json:"tokens" example:"1200"`
}

// DocumentPages is the response of GET /v1/documents/{id}/pages.
type DocumentPages struct {
	Document Document     `json:"document"`
	Pages    []StoredPage `json:"pages"`
}

// StoredPageFromDomain converts a stored page.
func StoredPageFromDomain(p domain.DocumentPage) StoredPage {
	return StoredPage{PageNo: p.PageNo, Markdown: p.Markdown, OCRText: p.OCRText, Numbers: p.Numbers,
		Unverified: p.Unverified, Error: p.Error, Tokens: p.Tokens}
}

// DocumentsAccepted is the non-streaming answer of POST
// /v1/documents/messages: the files are being read; ask on the session once
// they are ready.
type DocumentsAccepted struct {
	Type      string       `json:"type" example:"documents_accepted"`
	Documents []Document   `json:"documents"`
	Session   SessionBrief `json:"session"`
}

// DocumentProgressEvent is the data of the `document_progress` SSE event a
// streamed question sends while the documents of its session are read.
type DocumentProgressEvent struct {
	Type      string     `json:"type" example:"document_progress"`
	Documents []Document `json:"documents"`
}
