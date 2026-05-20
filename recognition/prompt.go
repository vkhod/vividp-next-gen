package recognition

// invoiceExtractionTool is the Claude tool definition for structured invoice extraction.
// Using tool use ensures Claude returns valid JSON even when uncertain.
const invoiceToolName = "extract_invoice"

const invoiceToolDescription = `Extract structured data from the provided document image(s).
First classify the document type. If it is an invoice, extract all available invoice fields.
If it is not an invoice, set document_type to the actual type and leave invoice fields empty.`

// InvoiceFields holds all extractable invoice fields.
// Zero values mean "not found" — do not guess.
type InvoiceFields struct {
	DocumentType string `json:"document_type"` // "invoice" | "receipt" | "contract" | "other" | ...
	Confidence   int    `json:"confidence"`     // 0-100: how confident the classification is

	// Invoice header
	InvoiceNumber string `json:"invoice_number,omitempty"`
	InvoiceDate   string `json:"invoice_date,omitempty"`   // ISO 8601 preferred
	DueDate       string `json:"due_date,omitempty"`
	PurchaseOrder string `json:"purchase_order,omitempty"`

	// Parties
	VendorName    string `json:"vendor_name,omitempty"`
	VendorAddress string `json:"vendor_address,omitempty"`
	VendorVAT     string `json:"vendor_vat,omitempty"`
	CustomerName  string `json:"customer_name,omitempty"`
	CustomerAddress string `json:"customer_address,omitempty"`
	CustomerVAT   string `json:"customer_vat,omitempty"`

	// Amounts
	Subtotal    string `json:"subtotal,omitempty"`    // before tax
	TaxAmount   string `json:"tax_amount,omitempty"`
	TaxRate     string `json:"tax_rate,omitempty"`    // e.g. "20%"
	TotalAmount string `json:"total_amount,omitempty"` // grand total
	Currency    string `json:"currency,omitempty"`    // ISO 4217, e.g. "USD"

	// Payment
	BankAccount string `json:"bank_account,omitempty"`
	IBAN        string `json:"iban,omitempty"`
	BIC         string `json:"bic,omitempty"`
	PaymentTerms string `json:"payment_terms,omitempty"`

	// Line items (best effort)
	LineItems []InvoiceLineItem `json:"line_items,omitempty"`
}

type InvoiceLineItem struct {
	Description string `json:"description"`
	Quantity    string `json:"quantity,omitempty"`
	UnitPrice   string `json:"unit_price,omitempty"`
	Amount      string `json:"amount,omitempty"`
}

// invoiceToolSchema returns the JSON schema for the tool input.
// This is the schema Claude uses to structure its output.
func invoiceToolSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"required": []string{"document_type", "confidence"},
		"properties": map[string]any{
			"document_type":    map[string]any{"type": "string", "description": "Document classification: invoice, receipt, contract, statement, other"},
			"confidence":       map[string]any{"type": "integer", "minimum": 0, "maximum": 100, "description": "Classification confidence 0-100"},
			"invoice_number":   map[string]any{"type": "string"},
			"invoice_date":     map[string]any{"type": "string"},
			"due_date":         map[string]any{"type": "string"},
			"purchase_order":   map[string]any{"type": "string"},
			"vendor_name":      map[string]any{"type": "string"},
			"vendor_address":   map[string]any{"type": "string"},
			"vendor_vat":       map[string]any{"type": "string"},
			"customer_name":    map[string]any{"type": "string"},
			"customer_address": map[string]any{"type": "string"},
			"customer_vat":     map[string]any{"type": "string"},
			"subtotal":         map[string]any{"type": "string"},
			"tax_amount":       map[string]any{"type": "string"},
			"tax_rate":         map[string]any{"type": "string"},
			"total_amount":     map[string]any{"type": "string"},
			"currency":         map[string]any{"type": "string"},
			"bank_account":     map[string]any{"type": "string"},
			"iban":             map[string]any{"type": "string"},
			"bic":              map[string]any{"type": "string"},
			"payment_terms":    map[string]any{"type": "string"},
			"line_items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"description": map[string]any{"type": "string"},
						"quantity":    map[string]any{"type": "string"},
						"unit_price":  map[string]any{"type": "string"},
						"amount":      map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}
