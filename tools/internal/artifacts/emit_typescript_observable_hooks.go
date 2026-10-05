// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

import "fmt"

func queryHookImports(view tsQuery, imports *tsImports) map[string]tsImport {
	keys := map[string]tsImport{}
	values := []string{"useQuery", "useSuspenseQuery", "QueryWhen"}
	types := []string{"PerformQuery", "SetSorting"}
	if view.Observable {
		values = []string{"useObservableQuery", "useSuspenseObservableQuery", "ObservableQueryWhen"}
		types = []string{}
		if view.Enumerable {
			types = append(types, "SetSorting")
		}
	}
	if view.Enumerable {
		if view.Observable {
			values = append(values, "useObservableQueryWithPaging", "useSuspenseObservableQueryWithPaging", "useChangeStream")
			keys["ChangeSet"] = imports.add("@cratis/arc/queries", "ChangeSet", false)
		} else {
			values = append(values, "useQueryWithPaging", "useSuspenseQueryWithPaging")
		}
		types = append(types, "SetPage", "SetPageSize")
		keys["Sorting"] = imports.add("@cratis/arc/queries", "Sorting", false)
		keys["Paging"] = imports.add("@cratis/arc/queries", "Paging", true)
	}
	for _, name := range values {
		keys[name] = imports.add("@cratis/arc.react/queries", name, true)
	}
	for _, name := range types {
		keys[name] = imports.add("@cratis/arc.react/queries", name, false)
	}
	keys["QueryResultWithState"] = imports.add("@cratis/arc/queries", "QueryResultWithState", false)
	return keys
}

// observableQueryHooks follows the captured 22.48.2 template, including its
// tuple projection for single results and paging generic choices. No transport
// or collection reconciliation is implemented by generated classes.
func observableQueryHooks(view tsQuery, keys map[string]tsImport, imports *tsImports) []string {
	alias := func(name string) string { return imports.aliases[keys[name]] }
	generic := view.Data + ", " + view.Name
	args, parameters, passed := "", "", ""
	if view.ParametersName != "" {
		args = "args?: " + view.ParametersName
		parameters = ", " + view.ParametersName
		generic += parameters
		passed = ", args"
	}
	if view.Enumerable {
		if args != "" {
			args += ", "
		}
		args += "sorting?: " + alias("Sorting")
		if passed == "" {
			passed = ", undefined"
		}
		passed += ", sorting"
	}
	tuple := "[" + alias("QueryResultWithState") + "<" + view.Data + ">"
	if view.Enumerable {
		tuple += ", " + alias("SetSorting")
	}
	tuple += "]"
	lines := []string{}
	for _, pair := range [][2]string{{"use", "useObservableQuery"}, {"useSuspense", "useSuspenseObservableQuery"}} {
		call := alias(pair[1]) + "<" + generic + ">(" + view.Name + passed + ");"
		body := "return " + call
		if !view.Enumerable {
			body = "const [result] = " + call + "\n        return [result];"
		}
		lines = append(lines, "static "+pair[0]+"("+args+"): "+tuple+" {\n        "+body+"\n    }")
	}
	if view.Enumerable {
		pagingTuple := "[" + alias("QueryResultWithState") + "<" + view.Data + ">, " + alias("SetSorting") + ", " + alias("SetPage") + ", " + alias("SetPageSize") + "]"
		for _, pair := range [][2]string{{"useWithPaging", "useObservableQueryWithPaging"}, {"useSuspenseWithPaging", "useSuspenseObservableQueryWithPaging"}} {
			lines = append(lines, "static "+pair[0]+"(pageSize: number, "+args+"): "+pagingTuple+" {\n        return "+alias(pair[1])+"<"+view.Data+", "+view.Name+">("+view.Name+", new "+alias("Paging")+"(0, pageSize)"+passed+");\n    }")
		}
		changeArgs, changePassed := "", ", undefined"
		if view.ParametersName != "" {
			changeArgs, changePassed = "args?: "+view.ParametersName+", ", ", args"
		}
		changeArgs += "getKey?: (item: " + view.Item + ") => unknown, sorting?: " + alias("Sorting")
		lines = append(lines, "static useChangeStream("+changeArgs+"): "+alias("ChangeSet")+"<"+view.Item+"> {\n        return "+alias("useChangeStream")+"<"+view.Item+", "+view.Name+parameters+">("+view.Name+changePassed+", getKey, sorting);\n    }")
	}
	when := alias("ObservableQueryWhen") + "<" + view.Name + ", " + view.Data + parameters + ">"
	lines = append(lines, "static when(condition: boolean): "+when+" {\n        return new "+when+"("+view.Name+", condition);\n    }")
	return lines
}

func observableQueryReserved(name string) bool {
	switch name {
	case "subscribe", "dispose", "validateArguments", "buildQueryArguments", "deserializeResult":
		return true
	}
	return false
}

// validateObservableIdentity accepts a supported scalar id or, as the C# client
// does, no id at all; identity-less elements use JSON/position fallbacks.
func validateObservableIdentity(model TypeDescriptor) error {
	identified := false
	for _, field := range model.Fields {
		if field.Name != "id" {
			continue
		}
		identified = true
		if field.Optional || field.Type.Nullable {
			continue
		}
		switch field.Type.Kind {
		case "string", "number", "Guid", "enum":
			return nil
		}
	}
	if !identified {
		return nil
	}
	return fmt.Errorf("observable collection requires a selected direct ID/Id serialized as id with a nonnullable scalar identity; other identity layouts are unsupported")
}
