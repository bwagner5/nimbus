package pretty

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"
)

type Prettifier[T any] interface {
	Prettify() T
}

// EncodeJSON takes any struct data and prints it in a pretty JSON format
func EncodeJSON(data any) string {
	var buffer bytes.Buffer
	enc := json.NewEncoder(&buffer)
	enc.SetIndent("", "    ")
	if err := enc.Encode(data); err != nil {
		panic(err)
	}
	return buffer.String()
}

// EncodeYAML takes any struct data and prints it in a pretty YAML format
func EncodeYAML(data any) string {
	jsonStr := EncodeJSON(data)
	// Convert the JSON to an object.
	var jsonObj interface{}
	// We are using yaml.Unmarshal here (instead of json.Unmarshal) because the
	// Go JSON library doesn't try to pick the right number type (int, float,
	// etc.) when unmarshalling to interface{}, it just picks float64
	// universally. go-yaml does go through the effort of picking the right
	// number type, so we can preserve number type throughout this process.
	err := yaml.Unmarshal([]byte(jsonStr), &jsonObj)
	if err != nil {
		panic("unable to render yaml")
	}
	// Marshal this object into YAML.
	out, err := yaml.Marshal(jsonObj)
	if err != nil {
		panic("unable to render yaml")
	}
	return string(out)
}

// Table takes any struct data and prints it in a table format
// The struct fields must have a `table` tag with the column name
// An optional `wide` tag can be added to the `table` tag to only show the column in wide mode
// Example:
//
//	type MyStruct struct {
//	    Field1 string `table:"Field 1"`
//	    Field2 string `table:"Field 2,wide"`
//	}
//
// pretty.Table([]MyStruct{{"test1", "test2"}}, false)
//
// Output:
//
// FIELD 1
// test1
//
// pretty.Table([]MyStruct{{"test1", "test2"}}, true)
//
// Output:
//
// FIELD 1     FIELD 2
// test1       test2
func Table[T any](data []T, wide bool) string {
	headers, rows := HeadersAndRows(data, wide)
	out := bytes.Buffer{}
	table := tablewriter.NewWriter(&out)
	table.SetHeader(headers)
	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("\t") // pad with tabs
	table.SetNoWhiteSpace(true)
	table.AppendBulk(rows) // Add Bulk Data
	table.Render()
	return out.String()
}

// HeadersAndRows is a helper to retrieve the headers and the rows from a slice of tagged structs
func HeadersAndRows[T any](data []T, wide bool) ([]string, [][]string) {
	var headers []string
	var rows [][]string
	for _, dataRow := range data {
		var row []string
		// clear headers each time so we only keep one set
		headers = []string{}
		reflectStruct := reflect.Indirect(reflect.ValueOf(dataRow))
		for i := 0; i < reflectStruct.NumField(); i++ {
			typeField := reflectStruct.Type().Field(i)
			tag := typeField.Tag.Get("table")
			if tag == "" {
				continue
			}
			subtags := strings.Split(tag, ",")
			if len(subtags) > 1 && subtags[1] == "wide" && !wide {
				continue
			}
			headers = append(headers, subtags[0])
			row = append(row, reflect.ValueOf(dataRow).Field(i).String())
		}
		rows = append(rows, row)
	}
	return headers, rows
}

func HeadersAndRowToStruct(headers []table.Column, row []string, result any) error {
	typeOfT := reflect.TypeOf(result).Elem()
	if typeOfT.Kind() != reflect.Struct {
		return errors.New("result must be a pointer to a struct")
	}

	if len(headers) != len(row) {
		return errors.New("headers and row must have the same length")
	}

	valueOfT := reflect.ValueOf(result).Elem()

	for i, header := range headers {
		for j := 0; j < typeOfT.NumField(); j++ {
			field := typeOfT.Field(j)
			if field.Tag.Get("table") == header.Title {
				fieldValue := valueOfT.Field(j)
				if fieldValue.CanSet() {
					fieldValue.SetString(row[i])
				}
			}
		}
	}

	return nil
}
