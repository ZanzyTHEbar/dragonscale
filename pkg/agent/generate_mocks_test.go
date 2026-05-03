package agent

//go:generate go tool mockgen -destination=mock_language_model_test.go -package=agent -mock_names=LanguageModel=MockLanguageModel charm.land/fantasy LanguageModel
