package tools

import (
	"testing"
)

func TestMapRunSpecFlatBuffer_RoundTrip(t *testing.T) {
	original := MapRunSpec{
		Version:          1,
		OperatorKind:     MapOperatorLLM,
		Instruction:      "extract label and priority",
		TaskTemplate:     "",
		OutputSchemaJSON: `{"type":"object"}`,
		MaxRetries:       3,
		DelegatedScope:   "",
		KeptWork:         "",
		OriginChannel:    "cli",
		OriginChatID:     "direct",
	}

	data, err := EncodeMapRunSpecFlatBuffer(original)
	if err != nil {
		t.Fatalf("encode run spec: %v", err)
	}
	decoded, err := DecodeMapRunSpecFlatBuffer(data)
	if err != nil {
		t.Fatalf("decode run spec: %v", err)
	}

	if decoded.OperatorKind != original.OperatorKind {
		t.Fatalf("operator kind mismatch: got %q want %q", decoded.OperatorKind, original.OperatorKind)
	}
	if decoded.Instruction != original.Instruction {
		t.Fatalf("instruction mismatch: got %q want %q", decoded.Instruction, original.Instruction)
	}
	if decoded.OutputSchemaJSON != original.OutputSchemaJSON {
		t.Fatalf("schema mismatch: got %q want %q", decoded.OutputSchemaJSON, original.OutputSchemaJSON)
	}
	if decoded.MaxRetries != original.MaxRetries {
		t.Fatalf("max retries mismatch: got %d want %d", decoded.MaxRetries, original.MaxRetries)
	}
}

func TestMapItemFlatBuffer_RoundTrip(t *testing.T) {
	input := MapItemInputRecord{
		Version:   1,
		ItemIndex: 7,
		ItemJSON:  `{"name":"alpha","priority":1}`,
		InputHash: "abc123",
	}
	inBuf, err := EncodeMapItemInputFlatBuffer(input)
	if err != nil {
		t.Fatalf("encode input: %v", err)
	}
	inDecoded, err := DecodeMapItemInputFlatBuffer(inBuf)
	if err != nil {
		t.Fatalf("decode input: %v", err)
	}
	if inDecoded.ItemIndex != input.ItemIndex || inDecoded.ItemJSON != input.ItemJSON || inDecoded.InputHash != input.InputHash {
		t.Fatalf("input mismatch: got %+v want %+v", inDecoded, input)
	}

	output := MapItemOutputRecord{
		Version:    1,
		ItemIndex:  7,
		Success:    true,
		Attempts:   2,
		OutputJSON: `{"label":"alpha"}`,
		ErrorText:  "",
		OutputHash: "def456",
	}
	outBuf, err := EncodeMapItemOutputFlatBuffer(output)
	if err != nil {
		t.Fatalf("encode output: %v", err)
	}
	outDecoded, err := DecodeMapItemOutputFlatBuffer(outBuf)
	if err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if outDecoded.ItemIndex != output.ItemIndex || outDecoded.Success != output.Success || outDecoded.Attempts != output.Attempts || outDecoded.OutputJSON != output.OutputJSON {
		t.Fatalf("output mismatch: got %+v want %+v", outDecoded, output)
	}
}
