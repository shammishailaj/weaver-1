package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type filters struct {
	Pid uint32
}

type functionTraceContext struct {
	binaryName   string
	Filters      filters
	FunctionName string
	HasArguments bool       // used for parsing text template
	Arguments    []argument `json:",omitempty"`
}

type argument struct {
	CType          string
	goType         goType
	StartingOffset int
	VariableName   string
	PrintfFormat   string
	TypeSize       int
	ArrayLength    int // Set as 0 if not array
	IsSlice        bool
	IsPointer      bool
}

type procInfo struct {
	Pid  uint32 `json:"pid,omitempty"`
	Ppid uint32 `json:"ppid,omitempty"`
	Comm string `json:"comm,omitempty"`
}

type modeOfOperation uint8

const (
	PACKAGE_MODE   modeOfOperation = 1
	FUNC_FILE_MODE modeOfOperation = 2
)

// unmarshalBinary for procInfo
func (i *procInfo) unmarshalBinary(data []byte) error {

	data = bytes.Trim(data, "\x00")
	// proc info struct is 24 bytes long and should at least be 8 bytes long
	if len(data) > 24 || len(data) < 8 {
		return fmt.Errorf("error decoding process info")
	}
	i.Pid = binary.LittleEndian.Uint32(data[0:4])
	i.Ppid = binary.LittleEndian.Uint32(data[4:8])
	i.Comm = string(data[8:])

	return nil
}

type goType int

const (
	INVALID        = 0
	INT     goType = iota
	INT8
	INT16
	INT32
	INT64
	UINT
	UINT8
	UINT16
	UINT32
	UINT64
	FLOAT32
	FLOAT64
	BOOL
	STRING
	BYTE
	RUNE
	POINTER
	//TODO:
	STRUCT
)

var goTypeToSizeInBytes = map[goType]int{
	INT:     8,
	INT8:    1,
	INT16:   2,
	INT32:   4,
	INT64:   8,
	UINT:    8,
	UINT8:   1,
	UINT16:  2,
	UINT32:  4,
	UINT64:  8,
	FLOAT32: 4,
	FLOAT64: 8,
	BOOL:    1,
	BYTE:    1,
	RUNE:    4,
	STRING:  8,
	POINTER: 8,
	//TODO:
	STRUCT: 8,
}

var goToCType = map[goType]string{
	INT:     "long",
	INT8:    "char",
	INT16:   "short",
	INT32:   "int",
	INT64:   "long",
	UINT:    "long",
	UINT8:   "char",
	UINT16:  "short",
	UINT32:  "int",
	UINT64:  "long",
	FLOAT32: "float",
	FLOAT64: "double",
	BOOL:    "char",
	BYTE:    "char",
	STRING:  "char *",
	RUNE:    "int",
	POINTER: "void *",
	//TODO:
	STRUCT: "void *",
}

func stringfFormat(t goType) string {
	switch t {
	case INT8, INT16, INT32, UINT8, UINT16, UINT32:
		return "%d"
	case INT, UINT, INT64, UINT64:
		return "%ld"
	case FLOAT32, FLOAT64:
		return "%e"
	case BOOL:
		return "%t"
	case STRING:
		return "%s"
	case BYTE:
		return "%c"
	case RUNE:
		return "%c"
	//TODO:
	case STRUCT, POINTER:
		return "0x%x"
	default:
		return "%v"
	}
}

var goTypeToString = map[goType]string{
	INT:     "INT",
	INT8:    "INT8",
	INT16:   "INT16",
	INT32:   "INT32",
	INT64:   "INT64",
	UINT:    "UINT",
	UINT8:   "UINT8",
	UINT16:  "UINT16",
	UINT32:  "UINT32",
	UINT64:  "UINT64",
	FLOAT32: "FLOAT32",
	FLOAT64: "FLOAT64",
	BOOL:    "BOOL",
	STRING:  "STRING",
	BYTE:    "BYTE",
	RUNE:    "RUNE",
	POINTER: "POINTER",
	//TODO:
	STRUCT: "STRUCT",
}

var supportedTypes = []string{
	"INT",
	"INT8",
	"INT16",
	"INT32",
	"INT64",
	"UINT",
	"UINT8",
	"UINT16",
	"UINT32",
	"UINT64",
	"FLOAT32",
	"FLOAT64",
	"BOOL",
	"STRING",
	"BYTE",
	"RUNE",
	"POINTER",
}

func listAvailableTypes() {
	for _, t := range supportedTypes {
		fmt.Fprintln(globalOutput, t)
	}
}

type stack []byte

func (s *stack) push(v byte) bool {

	if v == ' ' {
		return true
	}

	*s = append(*s, v)
	return true
}

func (s *stack) clear() {
	*s = []byte{}
}

func (s *stack) string() string {
	return string(*s)
}

// parseFunctionAndArgumentTypes populates the functionTraceContext based on the function and argument types
// of the form 'func_name(type1, type2)'.
func parseFunctionAndArgumentTypes(context *functionTraceContext, funcAndArgs string) error {

	parseStack := &stack{}

	var invalidChars = "+&%$#@!^<>?\";:{}=-`~" //fixme: this isn't exhaustive, doesn't take into account digits as first char
	argumentNumber := 0

	completed := false

	for i := range funcAndArgs {

		if strings.ContainsAny(string(funcAndArgs[i]), invalidChars) {
			return fmt.Errorf("encountered invalid char: %s", string(funcAndArgs[i]))
		}

		if funcAndArgs[i] == '(' {
			context.FunctionName = parseStack.string()
			invalidChars += string('(')
			parseStack.clear()
			continue
		}

		if funcAndArgs[i] == ',' || funcAndArgs[i] == ')' {
			var arg argument
			argumentNumber += 1
			arg.VariableName = fmt.Sprintf("argument%d", argumentNumber)
			err := populateArgumentValues(parseStack, &arg)
			if err != nil {
				return err
			}
			context.Arguments = append(context.Arguments, arg)

			if funcAndArgs[i] == ',' {
				parseStack.clear()
				continue
			}

			completed = true
			return nil
		}

		parseStack.push(funcAndArgs[i])
	}

	if !completed {
		return fmt.Errorf("incomplete function signature: %s", funcAndArgs)
	}

	return nil
}

func populateArgumentValues(parseStack *stack, arg *argument) error {

	if strings.Contains(parseStack.string(), "[]") {
		goType, err := parseSliceString(parseStack.string())
		if err != nil {
			return err
		}

		arg.IsSlice = true
		arg.goType = goType
		arg.PrintfFormat = stringfFormat(goType)
		arg.CType = goToCType[goType]
	} else if strings.Contains(parseStack.string(), "[") {
		length, goType, err := parseArrayString(parseStack.string())
		if err != nil {
			return err
		}
		arg.ArrayLength = length
		arg.goType = goType
		arg.PrintfFormat = stringfFormat(goType)
		arg.CType = goToCType[goType]
	} else if strings.HasPrefix(parseStack.string(), "*") {
		goType := POINTER
		arg.goType = goType
		arg.PrintfFormat = stringfFormat(goType)
		arg.CType = goToCType[goType]
	} else {
		goType := stringToGoType(parseStack.string())
		if goType == INVALID {
			return fmt.Errorf("invalid go type: %s", parseStack.string())
		}
		arg.goType = goType
		arg.PrintfFormat = stringfFormat(goType)
		arg.CType = goToCType[goType]
	}

	return nil
}

func parseArrayString(s string) (int, goType, error) {
	subs := strings.Split(s, "[")
	if len(subs) != 2 && subs[0] != "" {
		return -1, INVALID, errors.New("malformed array parameter")
	}

	subs = strings.Split(subs[1], "]")
	if len(subs) != 2 {
		return -1, INVALID, errors.New("malformed array parameter")
	}

	length, err := strconv.Atoi(subs[0])
	if err != nil {
		return -1, INVALID, errors.New("malformed array length")

	}

	gotype := stringToGoType(subs[1])
	if gotype == INVALID {
		return -1, INVALID, errors.New("malformed array type")
	}

	return length, gotype, nil
}

func parseSliceString(s string) (goType, error) {
	subs := strings.Split(s, "]")
	if len(subs) != 2 && subs[0] != "[" {
		return INVALID, errors.New("malformed array parameter")
	}

	goType := stringToGoType(subs[1])
	if goType == INVALID {
		return INVALID, errors.New("malformed slice type")
	}

	return goType, nil
}

func stringToGoType(typeString string) goType {

	typeString = strings.ToUpper(typeString)

	var stringToGoType = map[string]goType{
		"INT":     INT,
		"INT8":    INT8,
		"INT16":   INT16,
		"INT32":   INT32,
		"INT64":   INT64,
		"UINT":    UINT,
		"UINT8":   UINT8,
		"UINT16":  UINT16,
		"UINT32":  UINT32,
		"UINT64":  UINT64,
		"FLOAT32": FLOAT32,
		"FLOAT64": FLOAT64,
		"BOOL":    BOOL,
		"STRING":  STRING,
		"BYTE":    BYTE,
		"RUNE":    RUNE,
		"POINTER": POINTER,
		//TODO:
		"STRUCT": STRUCT,
	}

	if strings.HasPrefix(typeString, "*") {
		return POINTER
	}

	return stringToGoType[typeString]

}
