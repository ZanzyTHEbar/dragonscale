package tools

import (
	"fmt"

	"github.com/ZanzyTHEbar/dragonscale/pkg/tools/mapopsfb"
	flatbuffers "github.com/google/flatbuffers/go"
)

type MapOperatorKind string

const (
	MapOperatorLLM     MapOperatorKind = "llm_map"
	MapOperatorAgentic MapOperatorKind = "agentic_map"
	mapPayloadVersion  uint16          = 1
)

type MapRunSpec struct {
	Version          uint16
	OperatorKind     MapOperatorKind
	Instruction      string
	TaskTemplate     string
	OutputSchemaJSON string
	MaxRetries       uint16
	DelegatedScope   string
	KeptWork         string
	OriginChannel    string
	OriginChatID     string
}

type MapItemInputRecord struct {
	Version   uint16
	ItemIndex uint32
	ItemJSON  string
	InputHash string
}

type MapItemOutputRecord struct {
	Version    uint16
	ItemIndex  uint32
	Success    bool
	Attempts   uint16
	OutputJSON string
	ErrorText  string
	OutputHash string
}

func EncodeMapRunSpecFlatBuffer(spec MapRunSpec) ([]byte, error) {
	if spec.OperatorKind == "" {
		return nil, fmt.Errorf("operator kind is required")
	}
	version := spec.Version
	if version == 0 {
		version = 1
	}
	maxRetries := spec.MaxRetries
	if maxRetries == 0 {
		maxRetries = 1
	}

	b := flatbuffers.NewBuilder(256)
	operatorKind := b.CreateString(string(spec.OperatorKind))
	instruction := createOptionalFBString(b, spec.Instruction)
	taskTemplate := createOptionalFBString(b, spec.TaskTemplate)
	outputSchemaJSON := createOptionalFBString(b, spec.OutputSchemaJSON)
	delegatedScope := createOptionalFBString(b, spec.DelegatedScope)
	keptWork := createOptionalFBString(b, spec.KeptWork)
	originChannel := createOptionalFBString(b, spec.OriginChannel)
	originChatID := createOptionalFBString(b, spec.OriginChatID)

	mapopsfb.MapRunSpecStart(b)
	mapopsfb.MapRunSpecAddVersion(b, version)
	mapopsfb.MapRunSpecAddOperatorKind(b, operatorKind)
	if instruction != 0 {
		mapopsfb.MapRunSpecAddInstruction(b, instruction)
	}
	if taskTemplate != 0 {
		mapopsfb.MapRunSpecAddTaskTemplate(b, taskTemplate)
	}
	if outputSchemaJSON != 0 {
		mapopsfb.MapRunSpecAddOutputSchemaJson(b, outputSchemaJSON)
	}
	mapopsfb.MapRunSpecAddMaxRetries(b, maxRetries)
	if delegatedScope != 0 {
		mapopsfb.MapRunSpecAddDelegatedScope(b, delegatedScope)
	}
	if keptWork != 0 {
		mapopsfb.MapRunSpecAddKeptWork(b, keptWork)
	}
	if originChannel != 0 {
		mapopsfb.MapRunSpecAddOriginChannel(b, originChannel)
	}
	if originChatID != 0 {
		mapopsfb.MapRunSpecAddOriginChatId(b, originChatID)
	}
	obj := mapopsfb.MapRunSpecEnd(b)
	b.Finish(obj)

	return b.FinishedBytes(), nil
}

func DecodeMapRunSpecFlatBuffer(buf []byte) (MapRunSpec, error) {
	if len(buf) < flatbuffers.SizeUint32 {
		return MapRunSpec{}, fmt.Errorf("flatbuffer payload too short")
	}
	specFB := mapopsfb.GetRootAsMapRunSpec(buf, 0)
	version := specFB.Version()
	if version != mapPayloadVersion {
		return MapRunSpec{}, fmt.Errorf("unsupported map run spec version %d", version)
	}
	spec := MapRunSpec{
		Version:          version,
		OperatorKind:     MapOperatorKind(string(specFB.OperatorKind())),
		Instruction:      string(specFB.Instruction()),
		TaskTemplate:     string(specFB.TaskTemplate()),
		OutputSchemaJSON: string(specFB.OutputSchemaJson()),
		MaxRetries:       specFB.MaxRetries(),
		DelegatedScope:   string(specFB.DelegatedScope()),
		KeptWork:         string(specFB.KeptWork()),
		OriginChannel:    string(specFB.OriginChannel()),
		OriginChatID:     string(specFB.OriginChatId()),
	}
	if spec.OperatorKind == "" {
		return MapRunSpec{}, fmt.Errorf("invalid map run spec: operator kind is empty")
	}
	return spec, nil
}

func EncodeMapItemInputFlatBuffer(rec MapItemInputRecord) ([]byte, error) {
	if rec.ItemJSON == "" {
		return nil, fmt.Errorf("item JSON is required")
	}
	version := rec.Version
	if version == 0 {
		version = 1
	}

	b := flatbuffers.NewBuilder(128)
	itemJSON := b.CreateString(rec.ItemJSON)
	inputHash := createOptionalFBString(b, rec.InputHash)

	mapopsfb.MapItemInputStart(b)
	mapopsfb.MapItemInputAddVersion(b, version)
	mapopsfb.MapItemInputAddItemIndex(b, rec.ItemIndex)
	mapopsfb.MapItemInputAddItemJson(b, itemJSON)
	if inputHash != 0 {
		mapopsfb.MapItemInputAddInputHash(b, inputHash)
	}
	obj := mapopsfb.MapItemInputEnd(b)
	b.Finish(obj)
	return b.FinishedBytes(), nil
}

func DecodeMapItemInputFlatBuffer(buf []byte) (MapItemInputRecord, error) {
	if len(buf) < flatbuffers.SizeUint32 {
		return MapItemInputRecord{}, fmt.Errorf("flatbuffer payload too short")
	}
	recFB := mapopsfb.GetRootAsMapItemInput(buf, 0)
	version := recFB.Version()
	if version != mapPayloadVersion {
		return MapItemInputRecord{}, fmt.Errorf("unsupported map item input version %d", version)
	}
	rec := MapItemInputRecord{
		Version:   version,
		ItemIndex: recFB.ItemIndex(),
		ItemJSON:  string(recFB.ItemJson()),
		InputHash: string(recFB.InputHash()),
	}
	if rec.ItemJSON == "" {
		return MapItemInputRecord{}, fmt.Errorf("invalid map item input: item JSON is empty")
	}
	return rec, nil
}

func EncodeMapItemOutputFlatBuffer(rec MapItemOutputRecord) ([]byte, error) {
	version := rec.Version
	if version == 0 {
		version = 1
	}

	b := flatbuffers.NewBuilder(128)
	outputJSON := createOptionalFBString(b, rec.OutputJSON)
	errorText := createOptionalFBString(b, rec.ErrorText)
	outputHash := createOptionalFBString(b, rec.OutputHash)

	mapopsfb.MapItemOutputStart(b)
	mapopsfb.MapItemOutputAddVersion(b, version)
	mapopsfb.MapItemOutputAddItemIndex(b, rec.ItemIndex)
	mapopsfb.MapItemOutputAddSuccess(b, rec.Success)
	mapopsfb.MapItemOutputAddAttempts(b, rec.Attempts)
	if outputJSON != 0 {
		mapopsfb.MapItemOutputAddOutputJson(b, outputJSON)
	}
	if errorText != 0 {
		mapopsfb.MapItemOutputAddErrorText(b, errorText)
	}
	if outputHash != 0 {
		mapopsfb.MapItemOutputAddOutputHash(b, outputHash)
	}
	obj := mapopsfb.MapItemOutputEnd(b)
	b.Finish(obj)
	return b.FinishedBytes(), nil
}

func DecodeMapItemOutputFlatBuffer(buf []byte) (MapItemOutputRecord, error) {
	if len(buf) < flatbuffers.SizeUint32 {
		return MapItemOutputRecord{}, fmt.Errorf("flatbuffer payload too short")
	}
	recFB := mapopsfb.GetRootAsMapItemOutput(buf, 0)
	version := recFB.Version()
	if version != mapPayloadVersion {
		return MapItemOutputRecord{}, fmt.Errorf("unsupported map item output version %d", version)
	}
	return MapItemOutputRecord{
		Version:    version,
		ItemIndex:  recFB.ItemIndex(),
		Success:    recFB.Success(),
		Attempts:   recFB.Attempts(),
		OutputJSON: string(recFB.OutputJson()),
		ErrorText:  string(recFB.ErrorText()),
		OutputHash: string(recFB.OutputHash()),
	}, nil
}

func createOptionalFBString(b *flatbuffers.Builder, value string) flatbuffers.UOffsetT {
	if value == "" {
		return 0
	}
	return b.CreateString(value)
}
