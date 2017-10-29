package idgenerator

var (
	Reserved  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 1}
	Timestamp = map[string]int64{"DefValue": -1, "Value": 0, "Length": 11}
	UserDef   = map[string]int64{"DefValue": 0, "Value": 0, "Length": 0}
	WorkerId  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 2}
	Sequence  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 0}
	RandomId  = map[string]int64{"DefValue": 0, "Value": 0, "Length": 4}
)

type GeneratorConfig struct {
	GType int8
	Unit  []*FormatUnit
}
type FormatUnit struct {
	FType    string // timestamp, reserved, userdef, workerid, sequence, randomid
	DefValue int64
	Value    int64
	Length   int64
}
