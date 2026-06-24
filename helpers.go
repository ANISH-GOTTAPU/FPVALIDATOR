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
	"unicode"
)

func validateGoFile(path string, errs *[]string) {
	fs := token.NewFileSet()
	f, err := parser.ParseFile(fs, path, nil, parser.ParseComments)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("%s: failed parsing", path))
		return
	}

	validateMixedCaps(path, fs, f, errs)

	validateAcronyms(path, fs, f, errs)

	if strings.HasSuffix(path, "_test.go") {
		validateTestFileStructure(path, f, errs)
	}

	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			line := fs.Position(fn.Pos()).Line

			if fn.Name.IsExported() && !strings.HasPrefix(fn.Name.Name, "Test") {
				if fn.Doc == nil {
					*errs = append(*errs, fmt.Sprintf("%s:%d: exported function %q must have doc comment", path, line, fn.Name.Name))
				} else {
					text := strings.TrimSpace(fn.Doc.Text())

					// Check if comment ends with a period
					if !strings.HasSuffix(text, ".") {
						*errs = append(*errs, fmt.Sprintf("%s:%d: function comment should end with '.'", path, line))
					}

					// Check if comment starts with exact function name (case-sensitive)
					if !strings.HasPrefix(text, fn.Name.Name) {
						*errs = append(*errs, fmt.Sprintf("%s:%d: doc comment for function %q should start with the function name(check for case sensitive)", path, line, fn.Name.Name))
					}
				}
			}

			// Check for assertion-like behavior in non-TestXXX helpers
			if !strings.HasPrefix(fn.Name.Name, "Test") {
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					if callExpr, ok := n.(*ast.CallExpr); ok {
						if sel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
							if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "t" {
								switch sel.Sel.Name {
								case "Error", "Errorf":
									*errs = append(*errs, fmt.Sprintf("%s:%d: helper function %q should not call t.%s directly; return error instead", path, line, fn.Name.Name, sel.Sel.Name))
								}
							}
						}
					}
					return true
				})
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

	if strings.HasSuffix(path, "_test.go") {
		validateMustUsage(path, fs, f, errs)
	}
	validateNestedAnonymousFuncs(path, fs, f, errs)
	checkMixedCaps(path, fs, f, errs)
	fileErrs := scanFileForPatterns(path)
	*errs = append(*errs, fileErrs...)

	validateCommentedCode(path, errs)
	validateUnusedParameters(path, errs)
	validateErrorsNewUsage(path, errs)
	validateUnusedStructFields(path, errs)
	validateHardcodedTimeout(path, errs)
	validateMixedGNMIBatchUsage(path, errs)
	validateHardcodedSubinterfaceIndex(path, errs)
	validateDeviationUsage(path, errs)
	validateFunctionCommentMatch(path, errs)
	validateVendorCheckInDeviation(path, errs)
	validateLogInsteadOfError(path, errs)
	validateContextUsage(path, errs)
	validateDeviationComment(path, errs)
	validateConfigurePoliciesSignature(path, errs)
	validateMagicNumbers(path, errs)
}

func validateNestedAnonymousFuncs(path string, fs *token.FileSet, f *ast.File, errs *[]string) {

	ast.Inspect(f, func(n ast.Node) bool {
		callExpr, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		for _, arg := range callExpr.Args {
			if funcLit, ok := arg.(*ast.FuncLit); ok {
				pos := fs.Position(funcLit.Pos())
				*errs = append(*errs, fmt.Sprintf("%s:%d: avoid nesting anonymous function inside call; defining the watch function seperately to improve the readability.", path, pos.Line))
			}
		}

		return true
	})
}

func validateMustUsage(path string, fs *token.FileSet, f *ast.File, errs *[]string) {
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		funcName := fn.Name.Name
		if strings.HasPrefix(funcName, "must") || strings.HasPrefix(funcName, "Must") {
			// Function already starts with mustXYZ, skip validation
			continue
		}

		line := fs.Position(fn.Pos()).Line
		usesMust := false
		usesFatalErr := false

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			// Detect mustXYZ usage
			if callExpr, ok := n.(*ast.CallExpr); ok {
				if ident, ok := callExpr.Fun.(*ast.Ident); ok {
					if strings.HasPrefix(ident.Name, "must") || strings.HasPrefix(ident.Name, "Must") {
						usesMust = true
					}
				}
			}

			// Detect if err != nil followed by t.Fatalf
			if ifStmt, ok := n.(*ast.IfStmt); ok {
				if cond, ok := ifStmt.Cond.(*ast.BinaryExpr); ok {
					if ident, ok := cond.X.(*ast.Ident); ok && ident.Name == "err" {
						if cond.Op == token.NEQ {
							// Check if body contains t.Fatalf
							ast.Inspect(ifStmt.Body, func(n ast.Node) bool {
								if callExpr, ok := n.(*ast.CallExpr); ok {
									if sel, ok := callExpr.Fun.(*ast.SelectorExpr); ok {
										if xIdent, ok := sel.X.(*ast.Ident); ok && xIdent.Name == "t" && sel.Sel.Name == "Fatalf" {
											usesFatalErr = true
										}
									}
								}
								return true
							})
						}
					}
				}
			}

			return true
		})

		if usesFatalErr && !usesMust {
			*errs = append(*errs, fmt.Sprintf("%s:%d: function %s should start with mustXYZ", path, line, funcName))
		}
	}
}

func validateAcronyms(path string, fs *token.FileSet, f *ast.File, errs *[]string) {

	// Define a list of known acronyms
	acronyms := []string{"DUT", "IP", "MAC", "ATE", "IPv4", "IPv6", "OTG"}

	acronymMap := make(map[string]string)
	for _, a := range acronyms {
		acronymMap[strings.ToLower(a)] = a
	}

	ast.Inspect(f, func(n ast.Node) bool {
		if ident, ok := n.(*ast.Ident); ok {
			name := ident.Name

			// Skip if all lowercase or all uppercase
			if name == strings.ToLower(name) || name == strings.ToUpper(name) {
				return true
			}

			// Split into camel case parts
			parts := splitCamelCase(name)

			// Only check if there are multiple parts
			if len(parts) < 2 {
				return true
			}

			for i, part := range parts {
				lower := strings.ToLower(part)
				if correct, exists := acronymMap[lower]; exists && part != correct {
					// Allow acronym if it's the first part and lowercase (e.g., dutPorts)
					if i == 0 && part == lower {
						continue
					}
					pos := fs.Position(ident.Pos())
					*errs = append(*errs, fmt.Sprintf(
						"%s:%d: improper acronym casing in identifier '%s', should use '%s' instead of '%s'",
						path, pos.Line, name, correct, part))
				}
			}
		}
		return true
	})
}

// splitCamelCase splits a camelCase or PascalCase string into its components.
func splitCamelCase(s string) []string {
	var parts []string
	runes := []rune(s)
	start := 0
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) && (i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
			parts = append(parts, string(runes[start:i]))
			start = i
		}
	}
	parts = append(parts, string(runes[start:]))
	return parts
}

func validateMixedCaps(path string, fs *token.FileSet, f *ast.File, errs *[]string) {
	// Regex: starts with lowercase, contains at least one uppercase letter
	mixedCapsRegex := regexp.MustCompile(`^[a-z]+[A-Z][A-Za-z0-9]*$`)

	ast.Inspect(f, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.VAR {
			return true
		}

		for _, spec := range decl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range valueSpec.Names {
				if !mixedCapsRegex.MatchString(name.Name) {
					pos := fs.Position(name.Pos())
					*errs = append(*errs, fmt.Sprintf("%s:%d: variable '%s' does not follow MixedCaps (e.g., otgAgg1)", path, pos.Line, name.Name))
				}
			}
		}
		return true
	})
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

func checkMixedCaps(path string, fset *token.FileSet, f *ast.File, errs *[]string) {
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
							if vs.Doc != nil {
								docText := strings.TrimSpace(vs.Doc.Text())
								if !strings.HasPrefix(docText, name) {
									*errs = append(*errs, fmt.Sprintf("%s:%d: doc comment for exported variable %q should start with the exact variable name (case-sensitive)", path, fset.Position(ident.Pos()).Line, name))
								}
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

func validateCommentedCode(root string, errs *[]string) error {
	var codeLikeCommentRE = regexp.MustCompile(
		`^\s*//\s*(` +
			// Control flow.
			`if\b|else\b|switch\b|case\b|default\b|select\b|` +
			`for\b|range\b|go\b|defer\b|` +
			`return\b|break\b|continue\b|goto\b|fallthrough\b|` +

			// Declarations.
			`func\b|type\b|struct\b|interface\b|const\b|var\b|` +

			// Built-ins.
			`append\(|make\(|new\(|copy\(|delete\(|close\(|panic\(|recover\(|` +

			// Assignment.
			`[A-Za-z_][A-Za-z0-9_]*\s*:=|` +
			`[A-Za-z_][A-Za-z0-9_]*\s*=|` +

			// Generic method call.
			`[A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*\(|` +

			// Generic function call.
			`[A-Za-z_][A-Za-z0-9_]*\(|` +

			// Composite literal.
			`&?[A-Za-z_][A-Za-z0-9_]*\{|` +

			// nil.
			`nil\b` +
			`)`,
	)
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNo := 0

		for scanner.Scan() {
			lineNo++
			line := scanner.Text()

			if codeLikeCommentRE.MatchString(line) {
				*errs = append(*errs, fmt.Sprintf("%s:%d: commented-out code detected: %s", path, lineNo, strings.TrimSpace(line)))
			}
		}

		return scanner.Err()
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateUnusedParameters(root string, errs *[]string) error {
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Type.Params == nil {
				continue
			}

			// Collect parameter names.
			params := map[string]token.Pos{}
			for _, field := range fn.Type.Params.List {
				for _, name := range field.Names {
					if name.Name == "_" {
						continue
					}
					params[name.Name] = name.Pos()
				}
			}

			if len(params) == 0 {
				continue
			}

			// Track parameter usage inside the function body.
			used := make(map[string]bool)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok {
					return true
				}
				if _, ok := params[id.Name]; ok {
					used[id.Name] = true
				}
				return true
			})

			for param := range params {
				if !used[param] {
					pos := fset.Position(params[param])
					*errs = append(*errs, fmt.Sprintf("%s:%d: parameter %q is declared but never used in function %q", pos.Filename, pos.Line, param, fn.Name.Name))
				}
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateErrorsNewUsage(root string, errs *[]string) error {
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}

			if pkg.Name == "errors" && sel.Sel.Name == "New" {
				pos := fset.Position(call.Pos())
				*errs = append(*errs, fmt.Sprintf("%s:%d: use fmt.Errorf instead of errors.New", pos.Filename, pos.Line))
			}

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateUnusedStructFields(root string, errs *[]string) error {
	type fieldInfo struct {
		File string
		Line int
		Name string
	}

	fields := make(map[string]fieldInfo)
	used := make(map[string]bool)

	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		// Collect every struct field.
		ast.Inspect(file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}

			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}

			for _, f := range st.Fields.List {
				for _, name := range f.Names {
					pos := fset.Position(name.Pos())
					key := ts.Name.Name + "." + name.Name

					fields[key] = fieldInfo{
						File: pos.Filename,
						Line: pos.Line,
						Name: key,
					}
				}
			}
			return true
		})

		// Mark fields initialized in composite literals.
		ast.Inspect(file, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}

			ident, ok := cl.Type.(*ast.Ident)
			if !ok {
				return true
			}

			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}

				keyIdent, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}

				key := ident.Name + "." + keyIdent.Name
				used[key] = true
			}

			return true
		})

		// Mark fields accessed using selectors.
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			for key := range fields {
				if strings.HasSuffix(key, "."+sel.Sel.Name) {
					used[key] = true
				}
			}

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	for key, f := range fields {
		if !used[key] {
			*errs = append(*errs, fmt.Sprintf("%s:%d: struct field %q is never used", f.File, f.Line, key))
		}
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateHardcodedTimeout(root string, errs *[]string) error {
	fset := token.NewFileSet()

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			for _, arg := range call.Args {
				if isHardcodedDuration(arg) {
					pos := fset.Position(arg.Pos())
					*errs = append(*errs,
						fmt.Sprintf("%s:%d: hardcoded timeout detected, use a named constant instead", pos.Filename, pos.Line))
				}
			}

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func isHardcodedDuration(expr ast.Expr) bool {
	switch e := expr.(type) {

	// 10*time.Minute
	// 5*time.Second
	// 2*time.Hour
	case *ast.BinaryExpr:
		if e.Op != token.MUL {
			return false
		}

		_, leftIsNum := e.X.(*ast.BasicLit)

		sel, rightIsSelector := e.Y.(*ast.SelectorExpr)
		if !leftIsNum || !rightIsSelector {
			return false
		}

		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "time" {
			return false
		}

		switch sel.Sel.Name {
		case "Nanosecond",
			"Microsecond",
			"Millisecond",
			"Second",
			"Minute",
			"Hour":
			return true
		}
	}

	return false
}

func validateMixedGNMIBatchUsage(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}

			var (
				hasBatch     bool
				hasImmediate bool
			)

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}

				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}

				pkg, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}

				switch pkg.Name {

				// gNMI APIs
				case "gnmi":
					switch sel.Sel.Name {

					// Batched APIs
					case "BatchUpdate",
						"BatchReplace",
						"BatchDelete":
						hasBatch = true

					// Immediate APIs
					case "Update",
						"Replace",
						"Delete",
						"Set":
						hasImmediate = true
					}

				}

				return true
			})

			if hasBatch && hasImmediate {
				pos := fset.Position(fn.Pos())
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: function %q mixes batched and immediate gNMI operations; use a single SetBatch for consistency",
						pos.Filename, pos.Line, fn.Name.Name))
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateHardcodedSubinterfaceIndex(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			var funcName string

			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				funcName = fun.Sel.Name

			case *ast.Ident:
				funcName = fun.Name

			default:
				return true
			}

			switch funcName {
			case "GetOrCreateSubinterface",
				"Subinterface",
				"NewOCSubInterface",
				"AssignToNetworkInstance":

				for _, arg := range call.Args {
					lit, ok := arg.(*ast.BasicLit)
					if !ok || lit.Kind != token.INT {
						continue
					}

					pos := fset.Position(arg.Pos())

					*errs = append(*errs,
						fmt.Sprintf(
							"%s:%d: hardcoded subinterface index %s passed to %s(); use the subinterface ID from attrs instead",
							pos.Filename, pos.Line, lit.Value, funcName))
				}
			}

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateDeviationUsage(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		// Skip cfgplugins package completely.
		if file.Name != nil && file.Name.Name == "cfgplugins" {
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "deviations" {
				return true
			}

			pos := fset.Position(call.Pos())

			*errs = append(*errs,
				fmt.Sprintf(
					"%s:%d: direct use of deviations.%s() detected; move this logic into cfgplugins to maintain test abstraction",
					pos.Filename,
					pos.Line,
					sel.Sel.Name,
				),
			)

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateFunctionCommentMatch(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			// Ignore init().
			if fn.Name.Name == "init" {
				continue
			}

			// Skip functions without documentation comments.
			if fn.Doc == nil || len(fn.Doc.List) == 0 {
				continue
			}

			// First comment line.
			comment := fn.Doc.List[0].Text

			switch {
			case strings.HasPrefix(comment, "//"):
				comment = strings.TrimSpace(strings.TrimPrefix(comment, "//"))
			case strings.HasPrefix(comment, "/*"):
				comment = strings.TrimSpace(strings.TrimPrefix(comment, "/*"))
				comment = strings.TrimSuffix(comment, "*/")
			}

			if comment == "" {
				continue
			}

			fields := strings.Fields(comment)
			if len(fields) == 0 {
				continue
			}

			firstWord := fields[0]

			// Exact match (case-sensitive).
			if firstWord != fn.Name.Name {
				pos := fset.Position(fn.Pos())

				*errs = append(*errs,
					fmt.Sprintf(
						"%s:%d: function comment should start with %q but starts with %q",
						pos.Filename,
						pos.Line,
						fn.Name.Name,
						firstWord,
					))
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateVendorCheckInDeviation(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		type blockRange struct {
			start token.Pos
			end   token.Pos
		}

		var deviationBlocks []blockRange

		// ------------------------------------------------------------------
		// Pass 1: Collect all "if deviations.Xxx(...)" block ranges.
		// ------------------------------------------------------------------
		ast.Inspect(file, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}

			call, ok := ifStmt.Cond.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "deviations" {
				return true
			}

			deviationBlocks = append(deviationBlocks, blockRange{
				start: ifStmt.Body.Pos(),
				end:   ifStmt.Body.End(),
			})

			return true
		})

		// ------------------------------------------------------------------
		// Pass 2: Find dut.Vendor() usages.
		// ------------------------------------------------------------------
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Vendor" {
				return true
			}

			// Match only *.Vendor()
			if _, ok := sel.X.(*ast.Ident); !ok {
				// Handles dut.Vendor()
			} else {
				// also acceptable
			}

			insideDeviation := false
			for _, b := range deviationBlocks {
				if call.Pos() >= b.start && call.Pos() <= b.end {
					insideDeviation = true
					break
				}
			}

			if insideDeviation {
				return true
			}

			pos := fset.Position(call.Pos())
			*errs = append(*errs,
				fmt.Sprintf("%s:%d: direct dut.Vendor() usage should be moved into a deviation",
					pos.Filename, pos.Line))

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateLogInsteadOfError(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			ifStmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}

			// Only consider comparison conditions.
			switch cond := ifStmt.Cond.(type) {
			case *ast.BinaryExpr:
				switch cond.Op {
				case token.NEQ,
					token.EQL,
					token.GTR,
					token.LSS,
					token.GEQ,
					token.LEQ:
				default:
					return true
				}
			default:
				return true
			}

			if len(ifStmt.Body.List) != 1 {
				return true
			}

			exprStmt, ok := ifStmt.Body.List[0].(*ast.ExprStmt)
			if !ok {
				return true
			}

			call, ok := exprStmt.X.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "t" {
				return true
			}

			switch sel.Sel.Name {
			case "Log", "Logf", "Logln":
				pos := fset.Position(call.Pos())
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: validation failure uses %s(); consider using t.Errorf() instead",
						pos.Filename,
						pos.Line,
						sel.Sel.Name))
			}

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateContextUsage(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}

			// Match: t.Context()
			if sel.Sel.Name != "Context" {
				return true
			}

			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "t" {
				return true
			}

			pos := fset.Position(call.Pos())
			*errs = append(*errs,
				fmt.Sprintf("%s:%d: avoid using t.Context(); use context.Background() or pass a context for Go 1.22/1.23 compatibility",
					pos.Filename,
					pos.Line))

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateDeviationComment(root string, errs *[]string) error {
	issueTrackerRE := regexp.MustCompile(`https://(issuetracker\.google\.com/\d+|partnerissuetracker\.corp\.google\.com/.*/issues/\d+)`)

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}

			// Only validate deviation accessor functions.
			if !strings.HasSuffix(fn.Name.Name, "Unsupported") {
				continue
			}

			pos := fset.Position(fn.Pos())

			if fn.Doc == nil {
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: deviation function %q is missing a documentation comment",
						pos.Filename,
						pos.Line,
						fn.Name.Name))
				continue
			}

			comment := fn.Doc.Text()

			// ------------------------------------------------------------------
			// Check issue tracker.
			// ------------------------------------------------------------------
			if !issueTrackerRE.MatchString(comment) {
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: deviation comment for %q is missing a \"Tracked at: https://issuetracker.google.com/<id>\" line",
						pos.Filename,
						pos.Line,
						fn.Name.Name))
			}

			// ------------------------------------------------------------------
			// Check incorrect OC path.
			// ------------------------------------------------------------------
			if strings.Contains(comment, "global-filter-policy") {
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: deviation comment for %q contains incorrect path \"global-filter-policy\"; use \"global-filter\"",
						pos.Filename,
						pos.Line,
						fn.Name.Name))
			}

			// ------------------------------------------------------------------
			// First comment line should start with function name.
			// ------------------------------------------------------------------
			first := ""
			if len(fn.Doc.List) > 0 {
				first = strings.TrimSpace(strings.TrimPrefix(fn.Doc.List[0].Text, "//"))
			}

			if !strings.HasPrefix(first, fn.Name.Name+" ") &&
				first != fn.Name.Name {
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: first comment line should start with %q",
						pos.Filename,
						pos.Line,
						fn.Name.Name))
			}
		}

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}

func validateConfigurePoliciesSignature(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip deviations.go files.
		if filepath.Base(path) == "deviations.go" {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		funcMap := collectFunctionInfo(file)

		validateFunctionSignatures(fset, funcMap, errs)
		validateHelperCalls(file, fset, funcMap, errs)

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}
	return nil
}

type functionInfo struct {
	Name        string
	HasTestingT bool
	ParamCount  int
	Decl        *ast.FuncDecl
	IsMethod    bool
}

// collectFunctionInfo collects metadata for all local functions in a Go file.
func collectFunctionInfo(file *ast.File) map[string]functionInfo {
	funcs := make(map[string]functionInfo)

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}

		info := functionInfo{
			Name: fn.Name.Name,
			Decl: fn,
		}

		if fn.Type.Params != nil {
			info.ParamCount = len(fn.Type.Params.List)
		}

		info.HasTestingT = functionHasTestingT(fn)
		info.IsMethod = fn.Recv != nil
		funcs[fn.Name.Name] = info
	}

	return funcs
}

// functionHasTestingT reports whether the first parameter of fn is
// t *testing.T.
func functionHasTestingT(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}

	param := fn.Type.Params.List[0]

	if len(param.Names) == 0 || param.Names[0].Name != "t" {
		return false
	}

	star, ok := param.Type.(*ast.StarExpr)
	if !ok {
		return false
	}

	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return pkg.Name == "testing" && sel.Sel.Name == "T"
}

// validateFunctionSignatures validates that helper functions accepting
// *testing.T declare it as the first parameter named "t".
func validateFunctionSignatures(
	fset *token.FileSet,
	funcs map[string]functionInfo,
	errs *[]string,
) {
	for _, info := range funcs {
		fn := info.Decl
		if fn == nil || fn.Name.Name == "TestMain" {
			continue
		}

		// Skip methods.
		if fn.Recv != nil {
			continue
		}

		// Skip functions without parameters.
		if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
			continue
		}

		if info.HasTestingT {
			if !hasTParameter(fn) {
				pos := fset.Position(fn.Pos())
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: function %q should have a parameter named t of type *testing.T",
						pos.Filename,
						pos.Line,
						fn.Name.Name))
			}
		} else {
			if !hasTParameter(fn) {
				pos := fset.Position(fn.Pos())
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: function %q should have a parameter named t",
						pos.Filename,
						pos.Line,
						fn.Name.Name))
			}
		}
	}
}

// hasTParameter returns true if the function has a parameter named "t".
// If requireTestingT is true, the parameter must be of type *testing.T.
func hasTParameter(fn *ast.FuncDecl) bool {
	if fn == nil || fn.Type == nil || fn.Type.Params == nil {
		return false
	}

	for _, field := range fn.Type.Params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}

		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			continue
		}

		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			continue
		}

		if pkg.Name == "testing" && sel.Sel.Name == "T" {
			// Verify one of the parameter names is "t".
			for _, name := range field.Names {
				if name.Name == "t" {
					return true
				}
			}
		}
	}

	return false
}

// validateHelperCalls validates that local helper functions expecting
// t *testing.T are always invoked with t as their first argument.
func validateHelperCalls(
	file *ast.File,
	fset *token.FileSet,
	funcs map[string]functionInfo,
	errs *[]string,
) {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				// Skip selector expressions:
				// gnmi.Replace(...)
				// t.Helper(...)
				// fmt.Sprintf(...)
				return true
			}

			callee, ok := funcs[ident.Name]
			if !ok {
				return true
			}

			if callee.IsMethod || !callee.HasTestingT {
				return true
			}

			// Find where t *testing.T appears in the callee signature.
			tIndex := -1
			paramIndex := 0

			if callee.Decl.Type.Params != nil {
				for _, field := range callee.Decl.Type.Params.List {
					for range field.Names {
						if isTestingTType(field.Type) {
							tIndex = paramIndex
							break
						}
						paramIndex++
					}
					if tIndex != -1 {
						break
					}
				}
			}

			if tIndex == -1 {
				return true
			}

			callPos := fset.Position(call.Pos())

			// Missing argument.
			if len(call.Args) <= tIndex {
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: function %q expects parameter t *testing.T",
						callPos.Filename,
						callPos.Line,
						ident.Name))
				return true
			}

			arg, ok := call.Args[tIndex].(*ast.Ident)
			if !ok || arg.Name != "t" {
				*errs = append(*errs,
					fmt.Sprintf("%s:%d: function %q should be called with t for parameter %d",
						callPos.Filename,
						callPos.Line,
						ident.Name,
						tIndex+1))
			}

			return true
		})
	}
}

func isTestingTType(expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}

	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}

	return pkg.Name == "testing" && sel.Sel.Name == "T"
}

func validateMagicNumbers(root string, errs *[]string) error {
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}

		// Collect constant names.
		constNames := make(map[string]bool)
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}

			for _, spec := range genDecl.Specs {
				vs := spec.(*ast.ValueSpec)
				for _, name := range vs.Names {
					constNames[name.Name] = true
				}
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {

			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.INT {
				return true
			}

			// Ignore common values.
			switch lit.Value {
			case "0", "1", "-1":
				return true
			}

			// Ignore constant declarations.
			parentConst := false
			ast.Inspect(file, func(parent ast.Node) bool {
				gen, ok := parent.(*ast.GenDecl)
				if ok && gen.Tok == token.CONST {
					if lit.Pos() >= gen.Pos() && lit.End() <= gen.End() {
						parentConst = true
						return false
					}
				}
				return true
			})
			if parentConst {
				return true
			}

			// Ignore array declarations.
			if arr, ok := n.(*ast.ArrayType); ok {
				_ = arr
				return true
			}

			pos := fset.Position(lit.Pos())

			*errs = append(*errs,
				fmt.Sprintf("%s:%d: magic number %s detected; define a named constant instead",
					pos.Filename,
					pos.Line,
					lit.Value))

			return true
		})

		return nil
	})

	if err != nil {
		return err
	}

	if len(*errs) > 0 {
		return fmt.Errorf(strings.Join(*errs, "\n"))
	}

	return nil
}
