package tools

// Full `go generate ./pkg/tools` also runs FlatBuffers generation and requires
// flatc. Use `go generate -run mockgen ./pkg/tools` to refresh test mocks only.
//go:generate go tool mockgen -destination=mock_language_model_test.go -package=tools -mock_names=LanguageModel=MockLanguageModel charm.land/fantasy LanguageModel
