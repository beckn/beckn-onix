package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/beckn-one/beckn-onix/pkg/model"
	"github.com/beckn-one/beckn-onix/pkg/plugin"
	"github.com/beckn-one/beckn-onix/pkg/plugin/definition"
)

// answeringStep is a step that produces a synchronous answer, the way a
// provider plugin does once it has called upstream itself.
type answeringStep struct {
	answer []byte
}

func (s *answeringStep) Run(ctx *model.StepContext) error {
	ctx.ResponseBody = s.answer
	return nil
}

// routeSettingStep sets a route, putting the request on the proxy path.
type routeSettingStep struct{}

func (s *routeSettingStep) Run(ctx *model.StepContext) error {
	target, err := url.Parse("http://upstream.invalid/get-daily")
	if err != nil {
		return err
	}
	ctx.Route = &model.Route{TargetType: "url", URL: target}
	return nil
}

var errStepFailed = errors.New("step failed")

const v2SelectBody = `{"context":{"version":"2.0.0","action":"select","messageId":"msg-1"}}`

func serve(t *testing.T, h *stdHandler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "/select", strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to build request: %v", err)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, req)
	return recorder
}

// The behaviour every existing module depends on: no ResponseBody means the
// generated ACK, unchanged. This is the regression guard for the whole change.
func TestServeHTTPWritesTheGeneratedAckWhenNoStepAnswers(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
		steps:        []definition.Step{},
	}

	recorder := serve(t, h, v2SelectBody)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
	var got model.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}
	if got.Message.Status != model.StatusACK {
		t.Errorf("status = %q, want %q", got.Message.Status, model.StatusACK)
	}
	if got.Message.MessageID != "msg-1" {
		t.Errorf("message id = %q, want %q", got.Message.MessageID, "msg-1")
	}
}

// A step that answered gets its answer written verbatim, in place of the ACK.
func TestServeHTTPWritesAStepsAnswerInPlaceOfTheAck(t *testing.T) {
	answer := []byte(`{"context":{"action":"on_select"},"message":{"catalogs":[]}}`)
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
		steps:        []definition.Step{&answeringStep{answer: answer}},
	}

	recorder := serve(t, h, v2SelectBody)

	if recorder.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Body.String(); got != string(answer) {
		t.Errorf("body = %q, want %q", got, string(answer))
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("content type = %q, want application/json", contentType)
	}
}

// An answer is only for the no-route path. A step that both answers and routes
// is contradicting itself, and routing wins because the proxy owns the response
// from that point on -- silently discarding one or the other would be worse.
func TestServeHTTPPrefersRoutingOverAnAnswer(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
		// proxy() reaches straight for httpClient.Transport, so a routed handler
		// without one panics rather than failing.
		httpClient: http.DefaultClient,
		steps:      []definition.Step{&answeringStep{answer: []byte(`{"answered":true}`)}, &routeSettingStep{}},
	}

	recorder := serve(t, h, v2SelectBody)

	if strings.Contains(recorder.Body.String(), `"answered"`) {
		t.Error("expected routing to own the response once a route is set")
	}
}

// A step that fails after answering must still NACK: a half-built answer is not
// an answer, and an error has to reach the caller as one.
func TestServeHTTPNacksWhenAStepFailsAfterAnswering(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
		steps: []definition.Step{
			&answeringStep{answer: []byte(`{"answered":true}`)},
			&mockFailStep{err: model.NewBadReqErr("", errStepFailed)},
		},
	}

	recorder := serve(t, h, v2SelectBody)

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), `"answered"`) {
		t.Error("expected a NACK, not the partial answer")
	}
}

// An empty answer is not an answer. A step that sets no body leaves the ACK
// exactly as it was.
func TestServeHTTPTreatsAnEmptyAnswerAsNoAnswer(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
		steps:        []definition.Step{&answeringStep{answer: []byte{}}},
	}

	recorder := serve(t, h, v2SelectBody)

	var got model.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}
	if got.Message.Status != model.StatusACK {
		t.Errorf("expected the generated ACK, got status %q", got.Message.Status)
	}
}

// Provider steps land in the same id-keyed map as plain steps, so two entries
// sharing an id would leave one silently overwritten -- a capability lost with
// no error anywhere. Refused at startup instead.
//
// With binding keys a list, one entry serves several capabilities, so a repeated
// id is now a mistake rather than the way to configure a second one.
func TestInitStepsRefusesTwoProviderStepsWithTheSameID(t *testing.T) {
	h := &stdHandler{moduleName: "test-module"}
	cfg := &Config{
		Plugins: PluginCfg{
			ProviderSteps: []plugin.Config{
				{ID: "mausamgram", Config: map[string]string{}},
				{ID: "mausamgram", Config: map[string]string{}},
			},
		},
	}

	err := h.initSteps(context.Background(), noopPluginManager{}, cfg)
	if err == nil {
		t.Fatal("expected two provider steps with the same id to be refused")
	}
	if !strings.Contains(err.Error(), "mausamgram") {
		t.Errorf("error %q should name the id that repeats", err)
	}
}

// --- an unanswered request in a provider module ------------------------------

// silentStep is the dispatch no-op: a provider step recognising the request as
// none of its business. Succeeding without answering is how several provider
// steps coexist in one pipeline.
type silentStep struct{}

func (s *silentStep) Run(*model.StepContext) error { return nil }

// A module that serves capabilities itself has no proxy behind it. When nothing
// answered and no route was set, nothing ever will: there is nobody to send a
// callback. An ACK there tells the caller "accepted, answer follows" and leaves
// it waiting forever, so this is a NACK.
func TestServeHTTPNacksAnUnansweredRequestInAProviderModule(t *testing.T) {
	h := &stdHandler{
		SubscriberID:     "test-sub",
		role:             model.RoleBPP,
		moduleName:       "test-module",
		steps:            []definition.Step{&silentStep{}},
		hasProviderSteps: true,
	}

	recorder := serve(t, h, v2SelectBody)

	if recorder.Code == http.StatusOK {
		t.Fatalf("status = 200 for a request nothing answered; that ACK promises a callback nobody will send")
	}
	var got model.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}
	if got.Message.Status == model.StatusACK {
		t.Errorf("status = %q, want a NACK", got.Message.Status)
	}
}

// The guard must not touch a request that was answered.
func TestServeHTTPStillWritesAnAnswerInAProviderModule(t *testing.T) {
	answer := []byte(`{"context":{"action":"on_select"}}`)
	h := &stdHandler{
		SubscriberID:     "test-sub",
		role:             model.RoleBPP,
		moduleName:       "test-module",
		steps:            []definition.Step{&answeringStep{answer: answer}},
		hasProviderSteps: true,
	}

	recorder := serve(t, h, v2SelectBody)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if recorder.Body.String() != string(answer) {
		t.Errorf("body = %s, want the step's own answer", recorder.Body.String())
	}
}

// A routed request is untouched even in a provider module. This is the ordinary
// case for a module that serves one capability itself and proxies everything
// else: the provider step passes through, the router sets a route, and the proxy
// owns the response -- so the ACK it produces means what it says.
func TestServeHTTPLeavesARoutedRequestAloneInAProviderModule(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBPP,
		moduleName:   "test-module",
		// proxy() reaches straight for httpClient.Transport, so a routed handler
		// without one panics rather than failing.
		httpClient:       http.DefaultClient,
		steps:            []definition.Step{&silentStep{}, &routeSettingStep{}},
		hasProviderSteps: true,
	}

	recorder := serve(t, h, v2SelectBody)

	// The route points at an unreachable host, so what comes back is the proxy's
	// own failure. What matters is that it is the proxy answering at all: the
	// guard belongs to the no-route branch and must not have fired.
	if strings.Contains(recorder.Body.String(), "NET_ENTITY_NOT_FOUND") {
		t.Errorf("body = %s -- the unanswered guard fired on a routed request", recorder.Body.String())
	}
}

// A module with no provider steps is untouched. Its ACK still means what it has
// always meant: a proxy or a publisher carries the work on from here.
func TestServeHTTPStillAcksInAModuleWithoutProviderSteps(t *testing.T) {
	h := &stdHandler{
		SubscriberID: "test-sub",
		role:         model.RoleBAP,
		moduleName:   "test-module",
		steps:        []definition.Step{&silentStep{}},
	}

	recorder := serve(t, h, v2SelectBody)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var got model.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode the response: %v", err)
	}
	if got.Message.Status != model.StatusACK {
		t.Errorf("status = %q, want the generated ACK", got.Message.Status)
	}
}

// The instrumentor shallow-copies the context in but copies only named fields
// back out. An answer written by an instrumented step has to survive that, or
// it works unwrapped and vanishes wrapped -- and wrapped is the default.
func TestInstrumentedStepCarriesAnAnswerBack(t *testing.T) {
	answer := []byte(`{"answered":true}`)
	instrumented, err := NewInstrumentedStep(&answeringStep{answer: answer}, "answer", "test-module")
	if err != nil {
		t.Fatalf("failed to instrument the step: %v", err)
	}

	ctx := &model.StepContext{Context: t.Context()}
	if err := instrumented.Run(ctx); err != nil {
		t.Fatalf("Run() returned an unexpected error: %v", err)
	}
	if string(ctx.ResponseBody) != string(answer) {
		t.Errorf("response body = %q, want %q -- the instrumentor dropped it", ctx.ResponseBody, answer)
	}
}

// signAck must cover what is actually sent. Signing the generated ACK while
// sending something else would put a valid signature over the wrong bytes.
func TestAckSignerSignsTheAnswerThatWillBeSent(t *testing.T) {
	signer := &mockSigner{returnSig: "sig-over-the-answer"}
	step, err := newAckSignerStep(signer, &mockKM{keyset: &model.Keyset{UniqueKeyID: "k1", SigningPrivate: "priv"}})
	if err != nil {
		t.Fatalf("failed to build the ack signer: %v", err)
	}

	ctx := &model.StepContext{
		Context:         t.Context(),
		SubID:           "test-sub",
		ProtocolVersion: model.ProtocolVersionV2,
		MessageID:       "msg-1",
		RespHeader:      http.Header{},
		ResponseBody:    []byte(`{"context":{"action":"on_select"}}`),
	}
	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() returned an unexpected error: %v", err)
	}

	if !signer.signAckCalled {
		t.Fatal("expected the answer to be signed")
	}
	if got := ctx.RespHeader.Get("Signature"); !strings.Contains(got, "sig-over-the-answer") {
		t.Errorf("Signature header = %q, want it to carry the answer's signature", got)
	}
	if string(signer.signedBody) != string(ctx.ResponseBody) {
		t.Errorf("signed %q, want the body that will be sent, %q", signer.signedBody, ctx.ResponseBody)
	}
}

// With no answer, signAck still covers the generated ACK, exactly as before.
func TestAckSignerStillSignsTheGeneratedAckWhenNoStepAnswered(t *testing.T) {
	signer := &mockSigner{returnSig: "sig-over-the-ack"}
	step, err := newAckSignerStep(signer, &mockKM{keyset: &model.Keyset{UniqueKeyID: "k1", SigningPrivate: "priv"}})
	if err != nil {
		t.Fatalf("failed to build the ack signer: %v", err)
	}

	ctx := &model.StepContext{
		Context:         t.Context(),
		SubID:           "test-sub",
		ProtocolVersion: model.ProtocolVersionV2,
		MessageID:       "msg-1",
		RespHeader:      http.Header{},
	}
	if err := step.RunOnResponse(ctx, nil); err != nil {
		t.Fatalf("RunOnResponse() returned an unexpected error: %v", err)
	}

	wantAck, err := buildAckBody(model.ProtocolVersionV2, "msg-1")
	if err != nil {
		t.Fatalf("failed to build the expected ack: %v", err)
	}
	if string(signer.signedBody) != string(wantAck) {
		t.Errorf("signed %q, want the generated ack %q", signer.signedBody, wantAck)
	}
}
