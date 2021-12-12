package auth

import (
	"context"
	"sort"
	"strings"
)

type scopeContextKey struct{}

func WithScopes(ctx context.Context, scopes ...string) context.Context {
	if len(scopes) == 0 {
		return ctx
	}
	scopes = append([]string{}, scopes...)
	return context.WithValue(ctx, scopeContextKey{}, scopes)
}

func GetScopes(ctx context.Context) []string {
	if scopes, ok := ctx.Value(scopeContextKey{}).([]string); ok {
		return append([]string{}, scopes...)
	}
	return nil
}

func CleanScopes(scopes []string) []string {
	var result []string

	// merge recognizable scopes
	resourceTypes := make(map[string]map[string]map[string]struct{})
	for _, scope := range scopes {
		// extract resource type
		i := strings.Index(scope, ":")
		if i == -1 {
			result = append(result, scope)
		}
		resourceType := scope[:i]

		// extract resource name and actions
		scope = scope[i+1:]
		i = strings.LastIndex(scope, ":")
		if i == -1 {
			result = append(result, scope)
		}
		resourceName := scope[:i]
		actions := scope[i+1:]
		if actions == "" {
			// drop scope since no action found
			continue
		}

		// add to the intermediate map for de-duplication
		namedActions := resourceTypes[resourceType]
		if namedActions == nil {
			namedActions = make(map[string]map[string]struct{})
			resourceTypes[resourceType] = namedActions
		}
		actionSet := namedActions[resourceName]
		if actionSet == nil {
			actionSet = make(map[string]struct{})
			namedActions[resourceName] = actionSet
		}
		for _, action := range strings.Split(actions, ",") {
			actionSet[action] = struct{}{}
		}
	}

	// reconstruct scopes
	for resourceType, namedActions := range resourceTypes {
		for resourceName, actionSet := range namedActions {
			var actions []string
			for action := range actionSet {
				actions = append(actions, action)
			}
			sort.Strings(actions)
			scope := resourceType + ":" + resourceName + ":" + strings.Join(actions, ",")
			result = append(result, scope)
		}
	}

	// sort and return
	sort.Strings(result)
	return result
}
