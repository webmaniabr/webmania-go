package webmania

import (
	"context"
	"encoding/json"
	f "github.com/webmaniabr/webmania-go/model/api1fiscal"
)

// IssueNfe adds the model-55 contract to the common API1 issuance operation.
func (c *NfeClient) IssueNfe(ctx context.Context, b f.Nfe55IssueRequest) (*Result[f.NfeIssueDocumentResponse], error) {
	return c.issueModel(ctx, b, "Nfe55IssueRequest")
}

// IssueNfce adds the model-65 contract to the common API1 issuance operation.
func (c *NfeClient) IssueNfce(ctx context.Context, b f.Nfce65IssueRequest) (*Result[f.NfeIssueDocumentResponse], error) {
	return c.issueModel(ctx, b, "Nfce65IssueRequest")
}
func (c *NfeClient) issueModel(ctx context.Context, b any, schema string) (*Result[f.NfeIssueDocumentResponse], error) {
	_, v, e := snapshot(b, c.base.t.requestLimit)
	if e != nil {
		return nil, e
	}
	if e = schemaCheck("api1_fiscal", map[string]any{"$ref": "#/components/schemas/" + schema}, v); e != nil {
		return nil, e
	}
	r, e := c.base.execute(ctx, "nfeIssueDocument", nil, map[string]any{}, b, FileAuth{})
	if e != nil {
		return nil, e
	}
	var out f.NfeIssueDocumentResponse
	if e = json.Unmarshal(r.raw, &out); e != nil {
		return nil, r.decodeError()
	}
	return &Result[f.NfeIssueDocumentResponse]{Value: out, Status: r.status}, nil
}
