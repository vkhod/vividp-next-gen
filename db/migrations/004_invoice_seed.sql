-- ── 004_invoice_seed.sql ─────────────────────────────────────────────────────
-- Seeds an invoice document type for the dev tenant/system.
-- This exercises the full settings infrastructure — real data, not throwaway code.
-- ─────────────────────────────────────────────────────────────────────────────

-- Document type: Invoice
-- Belongs to the dev system (00000000-0000-0000-0000-000000000002)
INSERT INTO document_types (
    id, system_id, name, code, extraction_strategy,
    is_default, is_job_form, is_global_form, active
)
VALUES (
    '00000000-0000-0000-0000-000000000010',
    '00000000-0000-0000-0000-000000000002',
    'Invoice',
    'INVOICE',
    'content_first',   -- LLM reads content directly, no zone templates
    FALSE, FALSE, FALSE, TRUE
)
ON CONFLICT (id) DO NOTHING;

-- Field definitions for the invoice document type.
-- Columns: document_type_id, name, description, field_order, field_type,
--          recognition_config, validation_config, display_config
INSERT INTO field_definitions (document_type_id, name, description, field_order, field_type, recognition_config, validation_config, display_config)
VALUES
    ('00000000-0000-0000-0000-000000000010', 'document_type',    'Document Type',    1,  'Alpha numeric', '{"llm_hint": "document classification: invoice|receipt|contract|other"}', '{}', '{"read_only": true}'),
    ('00000000-0000-0000-0000-000000000010', 'invoice_number',   'Invoice Number',   2,  'Alpha numeric', '{"llm_hint": "invoice or bill number"}',                   '{"value_required": true}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'invoice_date',     'Invoice Date',     3,  'Date',          '{"llm_hint": "date the invoice was issued, ISO 8601"}',    '{"value_required": true, "date_type": "ISO8601"}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'due_date',         'Due Date',         4,  'Date',          '{"llm_hint": "payment due date"}',                         '{"date_type": "ISO8601"}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'purchase_order',   'PO Number',        5,  'Alpha numeric', '{"llm_hint": "purchase order number if present"}',         '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'vendor_name',      'Vendor Name',      6,  'Alpha numeric', '{"llm_hint": "name of the company issuing the invoice"}',  '{"value_required": true}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'vendor_address',   'Vendor Address',   7,  'Alpha numeric', '{"llm_hint": "full address of the vendor"}',               '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'vendor_vat',       'Vendor VAT',       8,  'Alpha numeric', '{"llm_hint": "vendor VAT or tax registration number"}',    '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'customer_name',    'Customer Name',    9,  'Alpha numeric', '{"llm_hint": "name of the company being billed"}',         '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'customer_address', 'Customer Address', 10, 'Alpha numeric', '{"llm_hint": "full address of the customer"}',             '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'customer_vat',     'Customer VAT',     11, 'Alpha numeric', '{"llm_hint": "customer VAT or tax registration number"}',  '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'subtotal',         'Subtotal',         12, 'Numeric',       '{"llm_hint": "total before tax, numeric value"}',          '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'tax_rate',         'Tax Rate',         13, 'Alpha numeric', '{"llm_hint": "tax percentage, e.g. 20%"}',                 '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'tax_amount',       'Tax Amount',       14, 'Numeric',       '{"llm_hint": "tax amount, numeric value"}',                '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'total_amount',     'Total Amount',     15, 'Numeric',       '{"llm_hint": "grand total including tax, numeric value"}', '{"value_required": true}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'currency',         'Currency',         16, 'Alpha numeric', '{"llm_hint": "ISO 4217 currency code, e.g. USD EUR GBP"}', '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'iban',             'IBAN',             17, 'Alpha numeric', '{"llm_hint": "IBAN for payment"}',                         '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'bic',              'BIC/SWIFT',        18, 'Alpha numeric', '{"llm_hint": "BIC or SWIFT code"}',                        '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'bank_account',     'Bank Account',     19, 'Alpha numeric', '{"llm_hint": "bank account number if IBAN not present"}',  '{}', '{}'),
    ('00000000-0000-0000-0000-000000000010', 'payment_terms',    'Payment Terms',    20, 'Alpha numeric', '{"llm_hint": "e.g. Net 30, immediate payment"}',           '{}', '{}')
ON CONFLICT DO NOTHING;
