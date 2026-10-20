package pfsenseapi

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/itsKontra/ddash/components/pfsense-api/pfsenseapipb"
)

// The alias writes. They share the static mappings' queue (c.write), read
// the aliases again inside it (an alias's id is its position) and send the
// alias's whole member list with "apply": true, which reloads the filter.
//
// pfSense's "append" and "remove" PATCH flags aren't used: they change a
// list only when the one sent differs from the stored one (so the only
// member can't be removed), and "remove" drops every description equal to
// the removed member's, which misaligns the descriptions of the others.
// Sending the list read a moment before, in the one queue, is exact.

const aliasPath = "/api/v2/firewall/alias"

// readAliases reads the aliases uncached; a write's lookup uses it.
func (c *component) readAliases(ctx context.Context) ([]alias, error) {
	var data []alias
	if err := c.api.get(ctx, "/api/v2/firewall/aliases", &data); err != nil {
		return nil, err
	}
	return data, nil
}

// lookupAlias reads the alias named name as it is now, and its id.
func (c *component) lookupAlias(ctx context.Context, name string) (*alias, int, error) {
	all, err := c.readAliases(ctx)
	if err != nil {
		return nil, -1, err
	}
	for id := range all {
		if all[id].Name == name {
			a := &all[id]
			// pfSense pads descriptions to the addresses; a list short of
			// them is padded here so positions keep matching.
			for len(a.Detail) < len(a.Address) {
				a.Detail = append(a.Detail, "")
			}
			return a, id, nil
		}
	}
	return nil, -1, status.Errorf(codes.NotFound, "pfSense has no alias %q", name)
}

// patchAlias replaces the alias's members with addresses and details and
// applies it, returning the alias as pfSense answers it.
func (c *component) patchAlias(ctx context.Context, id int, addresses, details []string) (*pfsenseapipb.Alias, error) {
	var updated alias
	body := map[string]any{"id": id, "address": addresses, "detail": details, "apply": true}
	if err := c.api.send(ctx, http.MethodPatch, aliasPath, body, &updated); err != nil {
		return nil, err
	}
	return updated.proto(), nil
}

func (c *component) AddAliasAddress(ctx context.Context, req *pfsenseapipb.AddAliasAddressRequest) (*pfsenseapipb.AddAliasAddressResponse, error) {
	address := strings.TrimSpace(req.GetAddress())
	if address == "" {
		return nil, status.Error(codes.InvalidArgument, "give the address to add")
	}
	var out *pfsenseapipb.Alias
	err := c.write(ctx, func() error {
		a, id, err := c.lookupAlias(ctx, req.GetAlias())
		if err != nil {
			return err
		}
		if slices.Contains(a.Address, address) {
			out = a.proto()
			return nil
		}
		details := a.Detail
		if d := req.GetDetail(); d != "" {
			details = append(details, d)
		} // else pfSense pads the last one with "Entry added <date>".
		out, err = c.patchAlias(ctx, id, append(a.Address, address), details)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &pfsenseapipb.AddAliasAddressResponse{Alias: out}, nil
}

func (c *component) RemoveAliasAddress(ctx context.Context, req *pfsenseapipb.RemoveAliasAddressRequest) (*pfsenseapipb.RemoveAliasAddressResponse, error) {
	address := strings.TrimSpace(req.GetAddress())
	var out *pfsenseapipb.Alias
	err := c.write(ctx, func() error {
		a, id, err := c.lookupAlias(ctx, req.GetAlias())
		if err != nil {
			return err
		}
		addresses, details := []string{}, []string{}
		for i, m := range a.Address {
			if m != address {
				addresses = append(addresses, m)
				details = append(details, a.Detail[i])
			}
		}
		if len(addresses) == len(a.Address) {
			out = a.proto()
			return nil
		}
		out, err = c.patchAlias(ctx, id, addresses, details)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &pfsenseapipb.RemoveAliasAddressResponse{Alias: out}, nil
}
