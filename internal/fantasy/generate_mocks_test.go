package fantasy

//go:generate go tool mockgen -source=model.go -destination=mock_language_model_test.go -package=fantasy
//go:generate go tool mockgen -source=tool_runtime.go -destination=mock_tool_runtime_test.go -package=fantasy
