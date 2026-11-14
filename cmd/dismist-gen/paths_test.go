package main

import (
	"bytes"
	"strings"
	"testing"
)

// A synthetic walk in gorewalk's format: anonymous structs, json tags,
// unexported fields, pointers, maps, arrays and custom decoders.
const syntheticWalk = `type config.EPConfig (github.com/example/config)
  ID                             string                                       
  Flag                           bool                                         json:",omitempty"
  Renamed                        string                                       json:"renamed_key,omitempty"
  Hidden                         string                                       json:"-"
  internal                       int                                          
  Items                          []config.Item                                
  State                          *config.State                                json:",omitempty"
  Names                          map[string]int                               
  XYZ                            [3]float64                                   
  When                           uuid.UUID                                    
  Anon                           struct { A bool; B *bool "json:\",omitempty\"" } 
  Opaque                         config.secret                                
type config.Item ()
  Band                           wifi.Band                                    
  Vlans                          []string                                     
type config.State ()
  Up                             bool                                         
type struct { A bool; B *bool "json:\",omitempty\"" } ()
  A                              bool                                         
  B                              *bool                                        json:",omitempty"
`

func TestGeneratePathsFromWalk(t *testing.T) {
	var out bytes.Buffer
	if err := generatePaths(strings.NewReader(syntheticWalk), &out, "config.EPConfig"); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(out.String(), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	want := []string{
		"ID\tstring\tstring",
		"Flag\tbool\tbool",
		"renamed_key\tstring\tstring",
		"Items\t[]config.Item\tslice",
		"Items[]\tconfig.Item\tstruct",
		"Items[].Band\twifi.Band\tcustom",
		"Items[].Vlans\t[]string\tslice",
		"Items[].Vlans[]\tstring\tstring",
		"State\t*config.State\tpointer",
		"State.Up\tbool\tbool",
		"Names\tmap[string]int\tmap",
		"Names{}\tint\tint",
		"XYZ\t[3]float64\tarray",
		"XYZ[]\tfloat64\tfloat",
		"When\tuuid.UUID\tcustom",
		"Anon\tstruct { A bool; B *bool \"json:\\\",omitempty\\\"\" }\tstruct",
		"Anon.A\tbool\tbool",
		"Anon.B\t*bool\tpointer",
		"Opaque\tconfig.secret\tnamed",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if err := generatePaths(strings.NewReader(syntheticWalk), &out, "config.Missing"); err == nil {
		t.Fatal("missing root accepted")
	}
	if keys := sortedKeys(map[string][]walkField{"b": nil, "a": nil}); keys[0] != "a" {
		t.Fatal("sortedKeys")
	}
}
