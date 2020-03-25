package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"reflect"
	"strconv"
	"strings"
)

type FuncGeneratorDescription struct {
	receiverTypeName       string
	inputBusinessParamName string
	funcName               string
	Url                    string `json:"url"`
	Auth                   bool   `json:"auth"`
	Method                 string `json:"method"`
}

type ValidateAttr struct {
	paramName    string
	isRequired   bool
	enumValues   []string
	defaultValue string
	min          int
	max          int
}

var structTypesToFunc = make(map[string][]FuncGeneratorDescription)

func main() {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, os.Args[1], nil, parser.ParseComments)
	if err != nil {
		log.Fatal(err)
	}
	out, _ := os.Create(os.Args[2])

	fmt.Fprintln(out, `// DO NOT CHANGE`)
	fmt.Fprintln(out)
	fmt.Fprintln(out, `package `+node.Name.Name)
	fmt.Fprintln(out)
	fmt.Fprintln(out, `import "net/http"`)
	fmt.Fprintln(out, `import "encoding/json"`)
	fmt.Fprintln(out, `import "errors"`)
	fmt.Fprintln(out, `import "fmt"`)
	fmt.Fprintln(out)

	fmt.Fprintln(out, `type DefaultResponseWrapper struct {`)
	fmt.Fprintln(out, "	Error		string "+"`"+`json:"error"`+"`")
	fmt.Fprintln(out, "	Response	interface{}"+"`"+`json:"response"`+"`")
	fmt.Fprintln(out, "}")
	fmt.Fprintln(out)

	for _, f := range node.Decls {
		g, ok := f.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if g.Recv == nil {
			continue
		}
		if g.Doc == nil {
			continue
		}

		if !strings.HasPrefix(g.Doc.Text(), "apigen:api") {
			continue
		}

		generatedStruct := FuncGeneratorDescription{}
		inParam := strings.TrimPrefix(g.Doc.Text(), "apigen:api ")

		err := json.Unmarshal([]byte(inParam), &generatedStruct)
		if err != nil {
			panic(err)
		}
		generatedStruct.funcName = g.Name.Name
		cur, ok := g.Recv.List[0].Type.(*ast.StarExpr)

		if !ok {
			generatedStruct.receiverTypeName = g.Recv.List[0].Type.(*ast.Ident).Name
			generatedStruct.inputBusinessParamName = g.Type.Params.List[1].Type.(*ast.Ident).Name
			structTypesToFunc[generatedStruct.receiverTypeName] = append(structTypesToFunc[generatedStruct.receiverTypeName], generatedStruct)
			continue
		}
		generatedStruct.receiverTypeName = cur.X.(*ast.Ident).Name
		generatedStruct.inputBusinessParamName = g.Type.Params.List[1].Type.(*ast.Ident).Name
		structTypesToFunc[generatedStruct.receiverTypeName] = append(structTypesToFunc[generatedStruct.receiverTypeName], generatedStruct)
	}
	prepeareServeHttpFuncForStructs(out, structTypesToFunc)

	// generate validation function for params with validate fields
	for _, f := range node.Decls {
		g, ok := f.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range g.Specs {
			currType, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			_, ok = currType.Type.(*ast.StructType)
			if !ok {
				continue
			}
			needToValidate := false
			for _, val := range structTypesToFunc {
				for _, val := range val {
					if currType.Name.Name == val.inputBusinessParamName {
						needToValidate = true
						break
					}
				}
				if needToValidate {
					break
				}
			}
			if !needToValidate {
				continue
			}

			buildValidationParamCode(out, currType)
		}
	}
}

func buildValidationParamCode(out *os.File, currType *ast.TypeSpec) {
	fmt.Fprintln(out, "func (srv *"+currType.Name.Name+") ValidateParams(r *http.Request) error {")
	currStruct, _ := currType.Type.(*ast.StructType)
	for _, filed := range currStruct.Fields.List {
		tagString, _ := reflect.StructTag(filed.Tag.Value[1 : len(filed.Tag.Value)-1]).Lookup("apivalidator")
		paramsList := strings.Split(tagString, ",")
		valParams := ValidateAttr{}
		for _, paramTag := range paramsList {
			if paramTag == "required" {
				valParams.isRequired = true
				continue
			}

			if strings.Contains(paramTag, "paramname=") {
				valParams.paramName = strings.TrimPrefix(paramTag, "paramname=")
			}

			if strings.Contains(paramTag, "default=") {
				valParams.defaultValue = strings.TrimPrefix(paramTag, "default=")
			}

			if strings.Contains(paramTag, "enum=") {
				valParams.enumValues = strings.Split(strings.TrimPrefix(paramTag, "enum="), "|")
			}
			if strings.Contains(paramTag, "min=") {
				minVal, err := strconv.Atoi(strings.TrimPrefix(paramTag, "min="))
				if err != nil {
					panic(err)
				}
				valParams.min = minVal
			}
			if strings.Contains(paramTag, "max=") {
				maxVal, err := strconv.Atoi(strings.TrimPrefix(paramTag, "max="))
				if err != nil {
					panic(err)
				}
				valParams.max = maxVal
			}
		}

		// check for param name
		fieldName := strings.ToLower(filed.Names[0].Name)
		if valParams.paramName != "" {
			fieldName = valParams.paramName
		}

		variableFieldName := strings.ToLower(filed.Names[0].Name)

		fmt.Fprintln(out, `	`+variableFieldName+`, ok := r.URL.Query()["`+fieldName+`"]`)

		// check if field required or not
		if valParams.isRequired {
			fmt.Fprintln(out, `	if ok == false {`)
			fmt.Fprintln(out, `		return ApiError{http.StatusBadRequest, fmt.Errorf("%s must me not empty", "`+fieldName+`")}`)
			fmt.Fprintln(out, `	}`)
		}

		filedType := filed.Type.(*ast.Ident).Name

		switch filedType {
		case "string":
			if len(valParams.enumValues) > 0 {
				fmt.Fprintln(out, "	isValueInEnum := false")
				fmt.Fprint(out, "	validatedEnum := []string{")
				for _, val := range valParams.enumValues {
					fmt.Fprint(out, `"`+val+`",`)
				}
				fmt.Fprint(out, "}")
				fmt.Fprintln(out)
				fmt.Fprintln(out, "	for _, val := range validatedEnum {")
				fmt.Fprintln(out, "		if "+variableFieldName+"[0] == val {")
				fmt.Fprintln(out, "			isValueInEnum = true")
				fmt.Fprintln(out, "		}")
				fmt.Fprintln(out, "	}")
				fmt.Fprintln(out, "	if isValueInEnum == false {")
				fmt.Fprint(out, `		return ApiError{http.StatusBadRequest, fmt.Errorf("%s must be one of [`+
					strings.Join(valParams.enumValues, ", ")+
					`]",`+fieldName+`)}`)
				fmt.Fprintln(out, "	}")
			}
			if valParams.defaultValue != "" {
				fmt.Fprintln(out, "	if "+variableFieldName+`[0] == "" {`)
				fmt.Fprintln(out, "		"+variableFieldName+"[0] = "+`"`+valParams.defaultValue+`"`)
				fmt.Fprintln(out, "	}")
			}
			if valParams.min != 0 {
				fmt.Fprintln(out, "	if len("+variableFieldName+`[0]) < `+strconv.Itoa(valParams.min)+` {`)
				fmt.Fprintln(out, `		return ApiError{http.StatusBadRequest, fmt.Errorf("%s must be >= %d", "`+fieldName+`",`+strconv.Itoa(valParams.min)+`)}`)
				fmt.Fprintln(out, `	}`)
			}
		case "int":
		default:
			panic("only string and int fields available")
		}

		//if filed.Type.(*ast.Ident).Name == "int" {
		//
		//}
	}
	fmt.Fprintln(out, "}")
	fmt.Fprintln(out)
}

func prepeareServeHttpFuncForStructs(out *os.File, structTypesToFunc map[string][]FuncGeneratorDescription) {
	for key, val := range structTypesToFunc {
		fmt.Fprintln(out, "func (srv *"+key+") ServeHTTP(w http.ResponseWriter, r *http.Request) {")
		fmt.Fprintln(out, "	switch r.URL.Path {")
		for _, val := range val {
			fmt.Fprintln(out, `	case "`+val.Url+`":`)
			fmt.Fprintln(out, "		srv.Wrap"+val.funcName+"(w, r)")
		}
		fmt.Fprintln(out, "	default:")
		fmt.Fprintln(out, "		w.WriteHeader(http.StatusNotFound)")
		fmt.Fprintln(out, "		json.NewEncoder(w).Encode("+"`{"+`"error": "unknown method"}`+"`)")
		fmt.Fprintln(out, "	}")
		fmt.Fprintln(out, "}")

		for _, val := range val {
			fmt.Fprintln(out)
			fmt.Fprintln(out, "func (srv *"+key+") Wrap"+val.funcName+"(w http.ResponseWriter, r *http.Request) {")
			fmt.Fprintln(out, "	ctx := r.Context()")
			fmt.Fprintln(out, "	inParam := "+val.inputBusinessParamName+"{}")
			fmt.Fprintln(out, "	err := inParam.ValidateParams(r)")
			fmt.Fprintln(out, "	var e *ApiError")
			fmt.Fprintln(out, "	if errors.Is(err, e) {")
			fmt.Fprintln(out, "		w.WriteHeader(e.HTTPStatus)")
			fmt.Fprintln(out, `		json.NewEncoder(w).Encode(fmt.Errorf(`+"`{"+`"error": %s}`+"`, e.Error()"+"))")
			fmt.Fprintln(out, "		return")
			fmt.Fprintln(out, "	}")
			fmt.Fprintln(out, "	res, err := srv."+val.funcName+"(ctx, inParam)")
			fmt.Fprintln(out, "	if errors.Is(err, e) {")
			fmt.Fprintln(out, "		w.WriteHeader(e.HTTPStatus)")
			fmt.Fprintln(out, `		json.NewEncoder(w).Encode(fmt.Errorf(`+"`{"+`"error": %s}`+"`, e.Error()"+"))")
			fmt.Fprintln(out, "		return")
			fmt.Fprintln(out, "	} else {")
			fmt.Fprintln(out, "		w.WriteHeader(http.StatusInternalServerError)")
			fmt.Fprintln(out, `		json.NewEncoder(w).Encode(fmt.Errorf(`+"`{"+`"error": %s}`+"`, err.Error()"+"))")
			fmt.Fprintln(out, "	}")
			fmt.Fprintln(out, "	response := DefaultResponseWrapper{}")
			fmt.Fprintln(out, "	response.Response = res")
			fmt.Fprintln(out, "	json.NewEncoder(w).Encode(response)")
			fmt.Fprintln(out, "}")
		}
		fmt.Fprintln(out)
	}
}
