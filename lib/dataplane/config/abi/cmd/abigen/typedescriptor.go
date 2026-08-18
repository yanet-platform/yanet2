package main

import (
	"debug/dwarf"
	"fmt"
	"strings"
)

func describeType(t dwarf.Type) (string, error) {
	t = stripTypedefsAndQuals(t)
	switch v := t.(type) {
	case nil:
		return "v", nil
	case *dwarf.VoidType:
		return "v", nil
	case *dwarf.UnspecifiedType:
		return "v", nil
	case *dwarf.BoolType:
		return fmt.Sprintf("b%d", v.ByteSize), nil
	case *dwarf.IntType:
		return fmt.Sprintf("i%d", v.ByteSize), nil
	case *dwarf.CharType:
		return fmt.Sprintf("i%d", v.ByteSize), nil
	case *dwarf.UintType:
		return fmt.Sprintf("u%d", v.ByteSize), nil
	case *dwarf.UcharType:
		return fmt.Sprintf("u%d", v.ByteSize), nil
	case *dwarf.FloatType:
		return fmt.Sprintf("f%d", v.ByteSize), nil
	case *dwarf.ComplexType:
		return fmt.Sprintf("c%d", v.ByteSize), nil
	case *dwarf.AddrType:
		return fmt.Sprintf("a%d", v.ByteSize), nil
	case *dwarf.PtrType:
		inner, err := describeType(v.Type)
		if err != nil {
			return "", err
		}

		return "*" + inner, nil
	case *dwarf.ArrayType:
		elem, err := describeType(v.Type)
		if err != nil {
			return "", err
		}

		if v.Count < 0 {
			return "[]" + elem, nil
		}

		return fmt.Sprintf("[%d]%s", v.Count, elem), nil
	case *dwarf.StructType:
		return describeAggregate(v)
	case *dwarf.EnumType:
		return describeEnum(v), nil
	case *dwarf.FuncType:
		return describeFunc(v)
	case *dwarf.DotDotDotType:
		return "...", nil
	default:
		return "", fmt.Errorf("unsupported DWARF type kind %T (%s) crosses the ABI boundary", t, t)
	}
}

func stripTypedefsAndQuals(t dwarf.Type) dwarf.Type {
	for {
		switch v := t.(type) {
		case *dwarf.TypedefType:
			t = v.Type
		case *dwarf.QualType:
			t = v.Type
		default:
			return t
		}
	}
}

func describeAggregate(v *dwarf.StructType) (string, error) {
	if v.StructName != "" {
		return v.Kind + " " + v.StructName, nil
	}

	parts := make([]string, 0, len(v.Field))
	for _, field := range v.Field {
		desc, err := describeType(field.Type)
		if err != nil {
			return "", err
		}

		parts = append(parts, fmt.Sprintf("%s:%s@%d", field.Name, desc, field.ByteOffset))
	}

	return fmt.Sprintf("%s{%s}", v.Kind, strings.Join(parts, ",")), nil
}

func describeEnum(v *dwarf.EnumType) string {
	if v.EnumName != "" {
		return "enum " + v.EnumName
	}

	return fmt.Sprintf("enum{u%d}", v.ByteSize)
}

func describeFunc(v *dwarf.FuncType) (string, error) {
	ret, err := describeType(v.ReturnType)
	if err != nil {
		return "", err
	}

	if len(v.ParamType) == 1 {
		if _, ok := v.ParamType[0].(*dwarf.DotDotDotType); ok {
			return fmt.Sprintf("fn(%s)(?)", ret), nil
		}
	}

	params := make([]string, 0, len(v.ParamType))
	for _, p := range v.ParamType {
		desc, err := describeType(p)
		if err != nil {
			return "", err
		}

		params = append(params, desc)
	}

	return fmt.Sprintf("fn(%s)(%s)", ret, strings.Join(params, ",")), nil
}
