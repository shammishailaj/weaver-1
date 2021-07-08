package main

import (
	"errors"
	"fmt"
	"io/ioutil"
	"strings"
)

func readFunctionsFile(path string) ([]functionTraceContext, error) {

	content, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read functions file")
	}

	functionStringsToTrace := removeDuplicates(strings.Split(string(content), "\n"))

	var contexts []functionTraceContext

	for _, funcString := range functionStringsToTrace {

		if funcString == "" || funcString == "\n" {
			continue
		}

		newContext := functionTraceContext{
			Filters:      filters{},
			HasArguments: true,
		}

		err := parseFunctionAndArgumentTypes(&newContext, funcString)
		if err != nil {
			return nil, fmt.Errorf("could not parse function string '%s': %s", funcString, err.Error())
		}

		err = determineStackOffsets(&newContext)
		if err != nil {
			return nil, fmt.Errorf("could not determine stack offsets of arguments: %s", err.Error())
		}

		contexts = append(contexts, newContext)
	}

	if contexts == nil {
		return nil, errors.New("no trace contexts created, empty file")
	}
	return contexts, nil
}

func removeDuplicates(elements []string) []string {
	encountered := map[string]bool{}

	for i := range elements {
		encountered[elements[i]] = true
	}

	result := []string{}
	for key, _ := range encountered {
		result = append(result, key)
	}
	return result
}
