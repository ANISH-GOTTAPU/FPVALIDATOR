package main

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func validateGoFile(path string, errs *[]string) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, path, nil, parser.ParseComments)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: failed parsing", path))
		return
	}

	if strings.HasSuffix(path, "_test.go") {
		validateTestFileStructure(path, f, errs)
	}

	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			line := fs.Position(fn.Pos()).Line

			if fn.Name.IsExported() && !strings.HasPrefix(fn.Name.Name, "Test") {
				if fn.Doc == nil {
					*errs = append(*errs, fmt.Sprintf("%s:%d: exported function %s must have doc comment", path, line, fn.Name.Name))
				} else {
					text := strings.TrimSpace(fn.Doc.Text())
					if !strings.HasSuffix(text, ".") {
						*errs = append(*errs, fmt.Sprintf("%s:%d: function comment should end with '.'", path, line))
					}
				}
			}

			if strings.HasPrefix(fn.Name.Name, "Get") {
				*errs = append(*errs, fmt.Sprintf("%s:%d: function %s should not use Get prefix", path, line, fn.Name.Name))
			}

			if strings.HasSuffix(path, "_test.go") &&
				fn.Recv == nil &&
				!strings.HasPrefix(fn.Name.Name, "Test") {

				var tName string
				if fn.Type.Params != nil {
					for _, param := range fn.Type.Params.List {
						if starExpr, ok := param.Type.(*ast.StarExpr); ok {
							if selExpr, ok := starExpr.X.(*ast.SelectorExpr); ok {
								if pkgIdent, ok := selExpr.X.(*ast.Ident); ok &&
									pkgIdent.Name == "testing" &&
									selExpr.Sel.Name == "T" {
									if len(param.Names) > 0 {
										tName = param.Names[0].Name
										break
									}
								}
							}
						}
					}
				}

				// Step 2: If tName is found, check for t.Helper() call
				if tName != "" {
					foundHelper := false
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						if ce, ok := n.(*ast.CallExpr); ok {
							if sel, ok := ce.Fun.(*ast.SelectorExpr); ok {
								if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == tName && sel.Sel.Name == "Helper" {
									foundHelper = true
								}
							}
						}
						return true
					})

					if !foundHelper {
						*errs = append(*errs, fmt.Sprintf("%s:%d: test helper function %s should call %s.Helper()", path, line, fn.Name.Name, tName))
					}
				}
			}

			if strings.HasSuffix(path, "_test.go") && fn.Recv == nil && !strings.HasPrefix(fn.Name.Name, "Test") {
				if len(fn.Name.Name) > 0 {
					firstChar := fn.Name.Name[0:1]
					if strings.ToUpper(firstChar) == firstChar {
						*errs = append(*errs, fmt.Sprintf("%s:%d: test function %s must start with lowercase letter", path, line, fn.Name.Name))
					}
				}
			}

			paramErrs := checkStructParameterUsage(path, fn, fs)
			*errs = append(*errs, paramErrs...)
		}
	}

	for _, obj := range f.Scope.Objects {
		if strings.Contains(obj.Name, "_") {
			pos := fs.Position(obj.Pos())
			*errs = append(*errs, fmt.Sprintf("%s:%d: identifier %s should not contain underscores", path, pos.Line, obj.Name))
		}
	}

	for _, decl := range f.Decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.VAR {
			for _, spec := range gd.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					if vs.Type != nil {
						typeName := fmt.Sprintf("%s", vs.Type)
						for _, name := range vs.Names {
							if strings.Contains(strings.ToLower(name.Name), strings.ToLower(typeName)) {
								pos := fs.Position(name.Pos())
								*errs = append(*errs, fmt.Sprintf("%s:%d: variable %s repeats its type %s in name", path, pos.Line, name.Name, typeName))
							}
						}
					}
				}
			}
		}
	}

	checkMixedCaps(path, f, errs)
	fileErrs := scanFileForPatterns(path)
	*errs = append(*errs, fileErrs...)
}

func validateTestFileStructure(path string, f *ast.File, errs *[]string) {
	hasTestMain := false
	var testFuncs []*ast.FuncDecl

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type == nil || fn.Body == nil {
			continue
		}

		// Check for TestMain function
		if fn.Name.Name == "TestMain" && fn.Type.Params != nil && len(fn.Type.Params.List) == 1 {
			if se, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr); ok {
				if ident, ok := se.X.(*ast.SelectorExpr); ok {
					if pkgIdent, ok := ident.X.(*ast.Ident); ok && pkgIdent.Name == "testing" && ident.Sel.Name == "M" {
						hasTestMain = true
					}
				}
			}
			continue
		}

		// Collect test functions
		if strings.HasPrefix(fn.Name.Name, "Test") {
			testFuncs = append(testFuncs, fn)
		}
	}

	if !hasTestMain {
		*errs = append(*errs, fmt.Sprintf("%s: missing TestMain function", path))
	}

	if len(testFuncs) == 0 {
		*errs = append(*errs, fmt.Sprintf("%s: no test functions found", path))
		return
	}

	if len(testFuncs) > 1 {
		*errs = append(*errs, fmt.Sprintf("%s: multiple top-level test functions found; please follow table-driven approach ref: https://go.dev/wiki/TableDrivenTests", path))
	}

	// Validate the single allowed test function
	mainTest := testFuncs[0]
	var hasSliceDecl, hasForLoop bool

	ast.Inspect(mainTest.Body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.DeclStmt:
			if genDecl, ok := stmt.Decl.(*ast.GenDecl); ok {
				for _, spec := range genDecl.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok {
						// Case 1: Explicit slice type
						if _, ok := vs.Type.(*ast.ArrayType); ok {
							hasSliceDecl = true
						}

						// Case 2: Inferred slice type via composite literal
						if vs.Type == nil {
							for _, val := range vs.Values {
								if cl, ok := val.(*ast.CompositeLit); ok {
									switch cl.Type.(type) {
									case *ast.ArrayType, *ast.Ident, *ast.SelectorExpr:
										hasSliceDecl = true
									}
								}
							}
						}
					}
				}
			}
		case *ast.AssignStmt:
			// Handle inferred slice via := assignment
			for _, rhs := range stmt.Rhs {
				if cl, ok := rhs.(*ast.CompositeLit); ok {
					switch cl.Type.(type) {
					case *ast.ArrayType, *ast.Ident, *ast.SelectorExpr:
						hasSliceDecl = true
					}
				}
			}
		case *ast.RangeStmt:
			hasForLoop = true
		}
		return true
	})

	if !(hasSliceDecl && hasForLoop) {
		*errs = append(*errs, fmt.Sprintf("%s: test function %s does not follow table-driven test pattern. Please follow table driven approach ref: https://go.dev/wiki/TableDrivenTests", path, mainTest.Name.Name))
	}
}

// Rule 9 & 18 scans
func scanFileForPatterns(path string) []string {
	f, _ := os.Open(path)
	defer f.Close()
	var errs []string
	scanner := bufio.NewScanner(f)
	lineNo := 1
	for scanner.Scan() {
		line := scanner.Text()
		// Rule 9: ban time.Sleep
		if strings.Contains(line, "time.Sleep(") {
			errs = append(errs, fmt.Sprintf("%s:%d: avoid time.Sleep, use gnmi.Watch", path, lineNo))
		}
		// Rule 18: cfgplugin funcs must return gnmi.SetRequest / Batch object
		// (strings.Contains(path, "cfgplugins") || strings.Contains(path, "dut_init"))
		if strings.Contains(path, "cfgplugins") && strings.Contains(line, "func") && strings.Contains(line, "{") {
			if !strings.Contains(line, "gnmi.SetRequest") && !strings.Contains(line, "gnmi.Batch") {
				errs = append(errs, fmt.Sprintf("%s:%d: cfgplugin function should return gnmi Batch/SetRequest", path, lineNo))
			}
		}
		// StringPiecelMeal: multiple string concatenation
		if strings.Contains(line, `" + "`) {
			errs = append(errs, fmt.Sprintf("%s:%d: avoid piecing strings with '+', use fmt.Sprintf or strings.Builder", path, lineNo))
		}

		// ErrorStrings: idiomatic error strings
		if strings.Contains(line, "t.Errorf(") || strings.Contains(line, "t.Error(") || strings.Contains(line, "fmt.Errorf(") {
			msg := extractStringLiteral(line)
			if msg != "" {
				if strings.HasPrefix(msg, strings.ToUpper(msg[:1])) {
					errs = append(errs, fmt.Sprintf("%s:%d: error string should not be capitalized", path, lineNo))
				}
				if strings.HasSuffix(msg, ".") {
					errs = append(errs, fmt.Sprintf("%s:%d: error string should not end with '.'", path, lineNo))
				}
			}
		}
		// // New rule: t.Log() should not have parameters
		// if strings.HasSuffix(path, "_test.go") {
		// 	if strings.Contains(line, "t.Log(") && !strings.HasSuffix(strings.TrimSpace(line), "t.Log()") {
		// 		errs = append(errs, fmt.Sprintf("%s:%d: t.Log() should be used without parameters, instead use t.Logf(); found: %s", path, lineNo, strings.TrimSpace(line)))
		// 	}
		// }
		// New rule: t.Log() / t.Logf() checks
		if strings.HasSuffix(path, "_test.go") {
			trimmed := strings.TrimSpace(line)
			// t.Log() must not have additional arguments
			tLogRe := regexp.MustCompile(`^t\.Log\((.*)\)$`)
			if m := tLogRe.FindStringSubmatch(trimmed); m != nil {
				args := m[1]
				// Check if there is a comma **outside quotes** to detect multiple arguments
				commaOutsideQuotes := false
				inQuotes := false
				for _, r := range args {
					if r == '"' {
						inQuotes = !inQuotes
					} else if r == ',' && !inQuotes {
						commaOutsideQuotes = true
						break
					}
				}
				if commaOutsideQuotes {
					errs = append(errs, fmt.Sprintf("%s:%d: t.Log() should not use multiple arguments: %s, instead use t.Logf()", path, lineNo, trimmed))
				}
			}

			// t.Logf() must have arguments after format string
			tLogfRe := regexp.MustCompile(`^t\.Logf\((.*)\)$`)
			if m := tLogfRe.FindStringSubmatch(trimmed); m != nil {
				args := m[1]
				// Split top-level commas (outside quotes)
				parts := []string{}
				inQuotes := false
				start := 0
				for i, r := range args {
					if r == '"' {
						inQuotes = !inQuotes
					} else if r == ',' && !inQuotes {
						parts = append(parts, strings.TrimSpace(args[start:i]))
						start = i + 1
					}
				}
				parts = append(parts, strings.TrimSpace(args[start:]))
				if len(parts) < 2 {
					errs = append(errs, fmt.Sprintf("%s:%d: t.Logf() must have arguments after format string: %s, instead use t.Log()", path, lineNo, trimmed))
				}
			}
		}
		lineNo++
	}
	return errs
}

// Rule 20: proto file must include bug URL
func checkProtoFiles(root string) []string {
	var errs []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".proto") {
			return nil
		}

		f, _ := os.Open(path)
		defer f.Close()
		scanner := bufio.NewScanner(f)

		// Pattern for bare bug IDs like: "sample b/123456789"
		bareBugRe := regexp.MustCompile(`\b\w+\s+b/(\d{9})\b`)

		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			matches := bareBugRe.FindStringSubmatch(line)
			if len(matches) == 2 {
				// Raise error suggesting full URL
				errs = append(errs, fmt.Sprintf("%s:%d: found bare bug ID %s, please use full URL like https://example.corp.example.com/issues/%s", path, lineNo, matches[1], matches[1]))
			}
		}

		return nil
	})
	return errs
}

// checkStructParameterUsage enforces struct parameter usage for functions
func checkStructParameterUsage(path string, fn *ast.FuncDecl, fs *token.FileSet) []string {
	var errs []string
	line := fs.Position(fn.Pos()).Line

	// Skip empty functions
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return errs
	}

	// Skip if only allowed params (*testing.T, *ondatra.DUTDevice)
	if len(fn.Type.Params.List) <= 2 && allParamsAllowed(fn.Type.Params.List) {
		return errs
	}

	nonStructCount := 0
	for _, param := range fn.Type.Params.List {
		typ := param.Type
		if isAllowedParam(typ) {
			continue
		}
		if !isStructType(typ) && !isPointerToStruct(typ) {
			nonStructCount++
		}
	}

	if nonStructCount > 1 {
		errs = append(errs, fmt.Sprintf("%s:%d: function %s has multiple parameters, consider using a single config struct", path, line, fn.Name.Name))
	}
	return errs
}

// allParamsAllowed returns true if all params are allowed (*testing.T, *ondatra.DUTDevice)
func allParamsAllowed(params []*ast.Field) bool {
	for _, param := range params {
		if !isAllowedParam(param.Type) {
			return false
		}
	}
	return true
}

// isAllowedParam skips *testing.T and *ondatra.DUTDevice
func isAllowedParam(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		if sel, ok := star.X.(*ast.SelectorExpr); ok {
			if sel.Sel.Name == "T" || sel.Sel.Name == "DUTDevice" {
				return true
			}
		}
	}
	return false
}

// isStructType checks if the type is a struct
func isStructType(expr ast.Expr) bool {
	_, ok := expr.(*ast.StructType)
	return ok
}

// isPointerToStruct checks if the type is a pointer to a struct
func isPointerToStruct(expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		_, ok2 := star.X.(*ast.StructType)
		return ok2
	}
	return false
}

// Extract string literal from errors.New("...")
func extractStringLiteral(line string) string {
	re := regexp.MustCompile(`"(.*?)"`)
	matches := re.FindStringSubmatch(line)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// MixedCaps regex rules
var (
	exportedMixedCaps   = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)
	unexportedMixedCaps = regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)
	snakeCase           = regexp.MustCompile(`_`)
	badAcronyms         = regexp.MustCompile(`Id|Url|Http`) // common violations
)

func checkMixedCaps(path string, f *ast.File, errs *[]string) {
	var fset = token.NewFileSet()
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			name := fn.Name.Name
			if snakeCase.MatchString(name) {
				*errs = append(*errs, fmt.Sprintf("%s:%d: function name %q should not use snake_case", path, fset.Position(fn.Pos()).Line, name))
			}
			if fn.Name.IsExported() {
				if !exportedMixedCaps.MatchString(name) {
					*errs = append(*errs, fmt.Sprintf("%s:%d: exported function name %q should use MixedCaps", path, fset.Position(fn.Pos()).Line, name))
				}
			} else {
				if !unexportedMixedCaps.MatchString(name) {
					*errs = append(*errs, fmt.Sprintf("%s:%d: unexported function name %q should use mixedCaps", path, fset.Position(fn.Pos()).Line, name))
				}
			}
			if badAcronyms.MatchString(name) {
				*errs = append(*errs, fmt.Sprintf("%s:%d: function name %q has mis-cased acronym (use ID/URL/HTTP)", path, fset.Position(fn.Pos()).Line, name))
			}
		}
		if gd, ok := decl.(*ast.GenDecl); ok {
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					name := ts.Name.Name
					if snakeCase.MatchString(name) {
						*errs = append(*errs, fmt.Sprintf("%s:%d: type name %q should not use snake_case", path, fset.Position(ts.Pos()).Line, name))
					}
					if ts.Name.IsExported() {
						if !exportedMixedCaps.MatchString(name) {
							*errs = append(*errs, fmt.Sprintf("%s:%d: exported type name %q should use MixedCaps", path, fset.Position(ts.Pos()).Line, name))
						}
					}
					if badAcronyms.MatchString(name) {
						*errs = append(*errs, fmt.Sprintf("%s:%d: type name %q has mis-cased acronym", path, fset.Position(ts.Pos()).Line, name))
					}
				}
				if vs, ok := spec.(*ast.ValueSpec); ok {
					for _, ident := range vs.Names {
						name := ident.Name
						if snakeCase.MatchString(name) {
							*errs = append(*errs, fmt.Sprintf("%s:%d: variable name %q should not use snake_case", path, fset.Position(ident.Pos()).Line, name))
						}
						if ident.IsExported() {
							if !exportedMixedCaps.MatchString(name) {
								*errs = append(*errs, fmt.Sprintf("%s:%d: exported var name %q should use MixedCaps", path, fset.Position(ident.Pos()).Line, name))
							}
						}
						if badAcronyms.MatchString(name) {
							*errs = append(*errs, fmt.Sprintf("%s:%d: variable name %q has mis-cased acronym", path, fset.Position(ident.Pos()).Line, name))
						}
					}
				}
			}
		}
	}
}
