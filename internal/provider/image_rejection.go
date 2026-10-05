package provider

import (
	"errors"
)

// Task 470: some OpenAI-compatible chat endpoints accept image_url content
// parts only as public http(s) URLs and reject inline base64 payloads (a
// `data:` URL) with an opaque 400 "Invalid request parameters" body that never
// names the image. Measured 2026-10-05 against api.xiaomimimo.com
// (mimo-v2.6-flash/pro): object-form, string-form and detail-annotated data
// URLs all 400 while the same payload as an http(s) URL returns 200 and the
// model describes the image. The request-side fact (the rejected request
// carried a data-URL image) is then the only diagnostic available, so the
// annotation below names the two real exits: turn the model's vision override
// off (attachments and view_image fall back to the image-understanding
// channel) or use an endpoint that accepts base64 data URLs.
//
// Callers pass whether the rejected request actually embedded a data-URL
// image; the provider package stays wire-shape agnostic.
func AnnotateInlineImageRejection(err error, hasInlineDataImages bool) error {
	if !hasInlineDataImages {
		return err
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || (apiErr.Status != 400 && apiErr.Status != 422) {
		return err
	}
	if apiErr.Hint != "" {
		return err
	}
	annotated := *apiErr
	annotated.Hint = "The rejected request carried inline image data (image_url with a data: URL). Some OpenAI-compatible endpoints accept image parts only as public http(s) URLs and reject base64 data URLs with an opaque 400 like this one. If this endpoint is one of them: set vision = false in this model's model_overrides so attachments and view_image fall back to the image-understanding channel, or use an endpoint that accepts inline base64 images."
	return &annotated
}
