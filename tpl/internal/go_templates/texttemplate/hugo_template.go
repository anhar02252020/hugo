// Copyright 2019 The Hugo Authors. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package template

import (
	"io"
	"reflect"

	"github.com/gohugoio/hugo/common/hreflect"

	"github.com/gohugoio/hugo/tpl/internal/go_templates/texttemplate/parse"
)

/*

This files contains the Hugo related addons. All the other files in this
package is auto generated.

*/

// Preparer prepares the template before execution.
type Preparer interface {
	Prepare() (*Template, error)
}

// ExecHelper allows some custom eval hooks.
type ExecHelper interface {
	GetFunc(tmpl Preparer, name string) (reflect.Value, bool)
	GetMethod(tmpl Preparer, receiver reflect.Value, name string) (method reflect.Value, firstArg reflect.Value)
	GetMapValue(tmpl Preparer, receiver, key reflect.Value) (reflect.Value, bool)
}

// Executer executes a given template.
type Executer interface {
	Execute(p Preparer, wr io.Writer, data interface{}) error
}

type executer struct {
	helper ExecHelper
}

func NewExecuter(helper ExecHelper) Executer {
	return &executer{helper: helper}
}

func (t *executer) Execute(p Preparer, wr io.Writer, data interface{}) error {
	tmpl, err := p.Prepare()
	if err != nil {
		return err
	}

	value, ok := data.(reflect.Value)
	if !ok {
		value = reflect.ValueOf(data)
	}

	state := &state{
		helper: t.helper,
		prep:   p,
		tmpl:   tmpl,
		wr:     wr,
		vars:   []variable{{"$", value}},
	}

	return tmpl.executeWithState(state, value)

}

// Prepare returns a template ready for execution.
func (t *Template) Prepare() (*Template, error) {
	return t, nil
}

func (t *Template) executeWithState(state *state, value reflect.Value) (err error) {
	defer errRecover(&err)
	if t.Tree == nil || t.Root == nil {
		state.errorf("%q is an incomplete or empty template", t.Name())
	}
	state.walk(value, t.Root)
	return
}

// Below are modifed structs etc. The changes are marked with "Added for Hugo."

// state represents the state of an execution. It's not part of the
// template so that multiple executions of the same template
// can execute in parallel.
type state struct {
	tmpl   *Template
	prep   Preparer   // Added for Hugo.
	helper ExecHelper // Added for Hugo.
	wr     io.Writer
	node   parse.Node // current node, for errors
	vars   []variable // push-down stack of variable values.
	depth  int        // the height of the stack of executing templates.
}

func (s *state) evalFunction(dot reflect.Value, node *parse.IdentifierNode, cmd parse.Node, args []parse.Node, final reflect.Value) reflect.Value {
	s.at(node)
	name := node.Ident

	var function reflect.Value
	var ok bool
	if s.helper != nil {
		// Added for Hugo.
		function, ok = s.helper.GetFunc(s.prep, name)
	}

	if !ok {
		function, ok = findFunction(name, s.tmpl)
	}

	if !ok {
		s.errorf("%q is not a defined function", name)
	}
	return s.evalCall(dot, function, cmd, name, args, final)
}

// evalField evaluates an expression like (.Field) or (.Field arg1 arg2).
// The 'final' argument represents the return value from the preceding
// value of the pipeline, if any.
func (s *state) evalField(dot reflect.Value, fieldName string, node parse.Node, args []parse.Node, final, receiver reflect.Value) reflect.Value {
	if !receiver.IsValid() {
		if s.tmpl.option.missingKey == mapError { // Treat invalid value as missing map key.
			s.errorf("nil data; no entry for key %q", fieldName)
		}
		return zero
	}
	typ := receiver.Type()
	receiver, isNil := indirect(receiver)
	if receiver.Kind() == reflect.Interface && isNil {
		// Calling a method on a nil interface can't work. The
		// MethodByName method call below would panic.
		s.errorf("nil pointer evaluating %s.%s", typ, fieldName)
		return zero
	}

	// Unless it's an interface, need to get to a value of type *T to guarantee
	// we see all methods of T and *T.
	ptr := receiver
	if ptr.Kind() != reflect.Interface && ptr.Kind() != reflect.Ptr && ptr.CanAddr() {
		ptr = ptr.Addr()
	}
	// Added for Hugo.
	var first reflect.Value
	var method reflect.Value
	if s.helper != nil {
		method, first = s.helper.GetMethod(s.prep, ptr, fieldName)
	} else {
		method = ptr.MethodByName(fieldName)
	}

	if method.IsValid() {
		if first != zero {
			return s.evalCall(dot, method, node, fieldName, args, final, first)
		}

		return s.evalCall(dot, method, node, fieldName, args, final)
	}

	hasArgs := len(args) > 1 || final != missingVal
	// It's not a method; must be a field of a struct or an element of a map.
	switch receiver.Kind() {
	case reflect.Struct:
		tField, ok := receiver.Type().FieldByName(fieldName)
		if ok {
			field := receiver.FieldByIndex(tField.Index)
			if tField.PkgPath != "" { // field is unexported
				s.errorf("%s is an unexported field of struct type %s", fieldName, typ)
			}
			// If it's a function, we must call it.
			if hasArgs {
				s.errorf("%s has arguments but cannot be invoked as function", fieldName)
			}
			return field
		}
	case reflect.Map:
		// If it's a map, attempt to use the field name as a key.
		nameVal := reflect.ValueOf(fieldName)
		if nameVal.Type().AssignableTo(receiver.Type().Key()) {
			if hasArgs {
				s.errorf("%s is not a method but has arguments", fieldName)
			}
			var result reflect.Value
			if s.helper != nil {
				// Added for Hugo.
				result, _ = s.helper.GetMapValue(s.prep, receiver, nameVal)
			} else {
				result = receiver.MapIndex(nameVal)
			}
			if !result.IsValid() {
				switch s.tmpl.option.missingKey {
				case mapInvalid:
					// Just use the invalid value.
				case mapZeroValue:
					result = reflect.Zero(receiver.Type().Elem())
				case mapError:
					s.errorf("map has no entry for key %q", fieldName)
				}
			}
			return result
		}
	case reflect.Ptr:
		etyp := receiver.Type().Elem()
		if etyp.Kind() == reflect.Struct {
			if _, ok := etyp.FieldByName(fieldName); !ok {
				// If there's no such field, say "can't evaluate"
				// instead of "nil pointer evaluating".
				break
			}
		}
		if isNil {
			s.errorf("nil pointer evaluating %s.%s", typ, fieldName)
		}
	}
	s.errorf("can't evaluate field %s in type %s", fieldName, typ)
	panic("not reached")
}

// evalCall executes a function or method call. If it's a method, fun already has the receiver bound, so
// it looks just like a function call. The arg list, if non-nil, includes (in the manner of the shell), arg[0]
// as the function itself.
func (s *state) evalCall(dot, fun reflect.Value, node parse.Node, name string, args []parse.Node, final reflect.Value, first ...reflect.Value) reflect.Value {
	if args != nil {
		args = args[1:] // Zeroth arg is function name/node; not passed to function.
	}
	typ := fun.Type()
	numFirst := len(first)
	numIn := len(args) + numFirst // // Added for Hugo
	if final != missingVal {
		numIn++
	}
	numFixed := len(args) + len(first)
	if typ.IsVariadic() {
		numFixed = typ.NumIn() - 1 // last arg is the variadic one.
		if numIn < numFixed {
			s.errorf("wrong number of args for %s: want at least %d got %d", name, typ.NumIn()-1, len(args))
		}
	} else if numIn != typ.NumIn() {
		s.errorf("wrong number of args for %s: want %d got %d", name, typ.NumIn(), numIn)
	}
	if !goodFunc(typ) {
		// TODO: This could still be a confusing error; maybe goodFunc should provide info.
		s.errorf("can't call method/function %q with %d results", name, typ.NumOut())
	}
	// Build the arg list.
	argv := make([]reflect.Value, numIn)
	// Args must be evaluated. Fixed args first.
	i := len(first)
	for ; i < numFixed && i < len(args)+numFirst; i++ {
		argv[i] = s.evalArg(dot, typ.In(i), args[i-numFirst])
	}
	// Now the ... args.
	if typ.IsVariadic() {
		argType := typ.In(typ.NumIn() - 1).Elem() // Argument is a slice.
		for ; i < len(args)+numFirst; i++ {
			argv[i] = s.evalArg(dot, argType, args[i-numFirst])
		}

	}
	// Add final value if necessary.
	if final != missingVal {
		t := typ.In(typ.NumIn() - 1)
		if typ.IsVariadic() {
			if numIn-1 < numFixed {
				// The added final argument corresponds to a fixed parameter of the function.
				// Validate against the type of the actual parameter.
				t = typ.In(numIn - 1)
			} else {
				// The added final argument corresponds to the variadic part.
				// Validate against the type of the elements of the variadic slice.
				t = t.Elem()
			}
		}
		argv[i] = s.validateType(final, t)
	}

	// Added for Hugo
	for i := 0; i < len(first); i++ {
		argv[i] = s.validateType(first[i], typ.In(i))
	}

	v, err := safeCall(fun, argv)
	// If we have an error that is not nil, stop execution and return that
	// error to the caller.
	if err != nil {
		s.at(node)
		s.errorf("error calling %s: %v", name, err)
	}
	if v.Type() == reflectValueType {
		v = v.Interface().(reflect.Value)
	}
	return v
}

func isTrue(val reflect.Value) (truth, ok bool) {
	return hreflect.IsTruthfulValue(val), true
}
