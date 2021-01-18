package main

import (
	"fmt"
	"strings"
)

// getTree creates a function that SSZ hashes the structs,
func (e *env) getTree(name string, v *Value) string {
	tmpl := `// GetTree returns tree-backing for the {{.name}} object
	func (:: *{{.name}}) GetTree() (*ssz.Node, error) {
		{{.getTree}}
	}`

	data := map[string]interface{}{
		"name":    name,
		"getTree": v.getTreeContainer(true),
	}
	str := execTmpl(tmpl, data)
	return appendObjSignature(str, v)
}

func (v *Value) getTrees(isList bool, elem Type) string {
	if elem != TypeUint {
		panic("unimplemented")
	}

	var merkleize string
	subLeavesTmpl := `subLeaves := ssz.LeavesFromUint64(::.{{.name}})`
	subLeaves := execTmpl(subLeavesTmpl, map[string]interface{}{
		"name": v.name,
	})

	if isList {
		tmpl := `numItems := len(::.{{.name}})
		tmp, err = ssz.TreeFromNodesWithMixin(subLeaves, numItems, int(ssz.CalculateLimit({{.listSize}}, uint64(numItems), {{.elemSize}})))
		if err != nil {
			return nil, err
		}`

		merkleize = execTmpl(tmpl, map[string]interface{}{
			"name":     v.name,
			"listSize": v.s,
			"elemSize": 8,
		})
	} else {
		merkleize = "tmp = ssz.TreeFromNodes(subLeaves)"
	}

	tmpl := `{
		{{.subLeaves}}
		{{.merkleize}}
	}`
	return execTmpl(tmpl, map[string]interface{}{
		"subLeaves": subLeaves,
		"merkleize": merkleize,
	})
}

func (v *Value) getTree() string {
	switch v.t {
	case TypeContainer, TypeReference:
		return v.getTreeContainer(false)

	case TypeBytes:
		// There are only fixed []byte
		name := v.name
		if v.c {
			name += "[:]"
		}

		tmpl := `{{.validate}}tmp = ssz.LeafFromBytes(::.{{.name}})`
		return execTmpl(tmpl, map[string]interface{}{
			"validate": v.validate("return nil, err"),
			"name":     name,
			"size":     v.s,
		})

	case TypeUint:
		var name string
		if v.ref != "" || v.obj != "" {
			// alias to Uint64
			name = fmt.Sprintf("uint64(::.%s)", v.name)
		} else {
			name = "::." + v.name
		}
		bitLen := v.n * 8
		return fmt.Sprintf("tmp = ssz.LeafFromUint%d(%s)", bitLen, name)

	case TypeBitList:
		panic("unimplemented")

	case TypeBool:
		return fmt.Sprintf("tmp = ssz.LeafFromBool(::.%s)", v.name)

	case TypeVector:
		return v.getTrees(false, v.e.t)

	case TypeList:
		if v.e.isFixed() {
			if v.e.t == TypeUint || v.e.t == TypeBytes {
				return v.getTrees(true, v.e.t)
			}
		}
		tmpl := `{
			num := uint64(len(::.{{.name}}))
			subLeaves := make([]*ssz.Node, num)
			if num > {{.num}} {
				err = ssz.ErrIncorrectListSize
				return nil, err
			}
			for i := uint64(0); i < num; i++ {
				n, err := ::.{{.name}}[i].GetTree()
				if err != nil {
					return nil, err
				}
				subLeaves[i] = n
			}
			tmp, err = ssz.TreeFromNodesWithMixin(subLeaves, len(subLeaves), {{.num}})
			if err != nil {
				return nil, err
			}
		}`
		return execTmpl(tmpl, map[string]interface{}{
			"name": v.name,
			"num":  v.m,
		})

	default:
		panic(fmt.Errorf("hash not implemented for type %s", v.t.String()))
	}
}

func (v *Value) getTreeContainer(start bool) string {
	if !start {
		return fmt.Sprintf("tmp, err = ::.%s.GetTree()\n if err != nil {\n return nil, err\n}", v.name)
	}

	numLeaves := nextPowerOfTwo(uint64(len(v.o)))
	out := []string{}
	for indx, i := range v.o {
		str := fmt.Sprintf("// Field (%d) '%s'\n%s\nleaves[%d] = tmp\n", indx, i.name, i.getTree(), indx)
		out = append(out, str)
	}
	// Empty leaves
	emptyLeaves := ""
	if numLeaves-uint(len(v.o)) > 0 {
		emptyLeaves = fmt.Sprintf("for i := 0; i < %d; i++ {\nleaves[i+%d] = ssz.EmptyLeaf()\n}", numLeaves-uint(len(v.o)), len(v.o))
	}

	tmpl := `leaves := make([]*ssz.Node, {{.numLeaves}})
	var tmp *ssz.Node
	var err error

	{{.fields}}
	{{.emptyLeaves}}

	return ssz.TreeFromNodes(leaves)`

	return execTmpl(tmpl, map[string]interface{}{
		"numLeaves":   numLeaves,
		"fields":      strings.Join(out, "\n"),
		"emptyLeaves": emptyLeaves,
	})
}

func nextPowerOfTwo(v uint64) uint {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return uint(v)
}