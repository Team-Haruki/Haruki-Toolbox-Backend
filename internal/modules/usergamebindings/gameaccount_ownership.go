package usergamebindings

import (
	"strconv"
	"strings"

	"haruki-suite/utils/database/postgresql"
)

type existingBindingState uint8

const (
	existingBindingStateNone existingBindingState = iota
	existingBindingStateOwnedByOther
	existingBindingStateVerifiedBySelf
)

func classifyExistingBinding(existing *postgresql.GameAccountBinding, userID string) existingBindingState {
	if existing == nil {
		return existingBindingStateNone
	}
	if ownerID := bindingOwnerID(existing); ownerID != "" && ownerID != userID {
		return existingBindingStateOwnedByOther
	}
	if isBindingOwnedByUser(existing, userID) && existing.Verified {
		return existingBindingStateVerifiedBySelf
	}
	return existingBindingStateNone
}

func bindingOwnerID(binding *postgresql.GameAccountBinding) string {
	if binding == nil || binding.Edges.User == nil {
		return ""
	}
	return strings.TrimSpace(binding.Edges.User.ID)
}

func bindingOwnerMissing(binding *postgresql.GameAccountBinding) bool {
	return bindingOwnerID(binding) == ""
}

func isBindingOwnedByUser(binding *postgresql.GameAccountBinding, userID string) bool {
	ownerID := bindingOwnerID(binding)
	if ownerID == "" {
		return false
	}
	return ownerID == strings.TrimSpace(userID)
}

func isNumericGameUserID(gameUserID string) bool {
	_, err := strconv.ParseInt(gameUserID, 10, 64)
	return err == nil
}
