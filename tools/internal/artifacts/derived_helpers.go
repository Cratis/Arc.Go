// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package artifacts

func hasDerivedModels(analysis *analysis) bool {
	for _, model := range analysis.exports {
		if model.d.targetInterface != "" {
			return true
		}
	}
	return false
}
