package bytecode

import (
	"fmt"
	"sort"
	"strings"
)

func FormatProgram(program *Program) string {
	if program == nil {
		return "program: <nil>\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "program:\n")
	fmt.Fprintf(&b, "  entry: %d\n", program.Entry)
	fmt.Fprintf(&b, "  init: %d\n", program.Init)
	fmt.Fprintf(&b, "  globals: %d\n", program.GlobalCount)

	if len(program.HostFunctions) > 0 {
		fmt.Fprintf(&b, "  host functions:\n")
		for i, host := range program.HostFunctions {
			fmt.Fprintf(&b, "    #%d %s params=%d results=%d variadic=%t\n", i, host.Name, host.Params, host.Results, host.Variadic)
		}
	}

	if len(program.StructSchemas) > 0 {
		names := make([]string, 0, len(program.StructSchemas))
		for name := range program.StructSchemas {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(&b, "  struct schemas:\n")
		for _, name := range names {
			schema := program.StructSchemas[name]
			fmt.Fprintf(&b, "    %s fields=%v kinds=%v\n", name, schema.FieldNames, schema.FieldKinds)
		}
	}

	fmt.Fprintf(&b, "  functions:\n")
	for i, fn := range program.Functions {
		fmt.Fprintf(&b, "    function #%d %s params=%d results=%d locals=%d maxStack=%d variadic=%t\n", i, fn.Name, fn.Params, fn.Results, fn.Locals, fn.MaxStack, fn.Variadic)
		for pc, inst := range fn.Code {
			fmt.Fprintf(&b, "      %04d %-36s A=%d B=%d I64=%d", pc, inst.Op.String(), inst.A, inst.B, inst.I64)
			if inst.S != "" {
				fmt.Fprintf(&b, " S=%q", inst.S)
			}
			fmt.Fprintf(&b, "\n")
		}
	}

	return b.String()
}
