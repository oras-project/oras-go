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
	scopes = CleanScopes(scopes)
	return context.WithValue(ctx, scopeContextKey{}, scopes)
}

func AppendScopes(ctx context.Context, scopes ...string) context.Context {
	if len(scopes) == 0 {
		return ctx
	}
	return WithScopes(ctx, append(GetScopes(ctx), scopes...)...)
}

func GetScopes(ctx context.Context) []string {
	if scopes, ok := ctx.Value(scopeContextKey{}).([]string); ok {
		return append([]string{}, scopes...)
	}
	return nil
}

func CleanScopes(scopes []string) []string {
	// fast paths
	switch len(scopes) {
	case 0:
		return nil
	case 1:
		scope := scopes[0]
		i := strings.LastIndex(scope, ":")
		if i == -1 {
			return []string{scope}
		}
		actionList := strings.Split(scope[i+1:], ",")
		actionList = cleanStrings(actionList)
		actions := strings.Join(actionList, ",")
		scope = scope[:i+1] + actions
		return []string{scope}
	}

	// slow path
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
			if action != "" {
				actionSet[action] = struct{}{}
			}
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

func cleanStrings(s []string) []string {
	sort.Strings(s)
	for i, j := 0, 1; j < len(s); j++ {
		if s[i] != s[j] {
			i++
			if i != j {
				s[i] = s[j]
			}
		}
	}
	return s
}
