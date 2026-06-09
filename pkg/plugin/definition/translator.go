package definition

import "context"

// Translator executes a single translation pass over a payload fragment using
// a fetched translation artifact. Implementations are stateless — all state
// required for translation is carried in the artifact bytes themselves.
//
// artifact is the raw bytes of the translation artifact fetched from
// schema.beckn.io (e.g. a JSONata expression). payload is the payload fragment
// to be translated. The translated fragment is returned as a new byte slice.
//
// The mediator selects the Translator implementation by matching the artifact's
// Content-Type, allowing a single mediation pass to dispatch different fragments
// to different translators (e.g. JSONata for one schema delta, SHACL for another).
type Translator interface {
	Translate(ctx context.Context, artifact []byte, payload []byte) ([]byte, error)
}

// TranslatorProvider initializes a Translator instance with its configuration.
type TranslatorProvider interface {
	New(ctx context.Context, config map[string]string) (Translator, func() error, error)
}
