package marketplace

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	perrors "github.com/mrlaoliai/polaris-harness/internal/errors"
	"github.com/mrlaoliai/polaris-harness/internal/protocol"
	"github.com/mrlaoliai/polaris-harness/pkg/substrate"
)

type InstallRequest struct {
	Principal   string
	ExtensionID string
	ExtType     string // plugin, skill, mcp
	TrustTier   int
	Publisher   string
	HasHooks    bool
}

var ErrRequiresApproval = errors.New("installation requires user approval")

type Manager struct {
	db                *sql.DB
	mcpMgr            any
	policyGate        protocol.PolicyGate
	prefsRepo         protocol.PreferencesRepo
	auditTrail        *substrate.AuditTrail
	publisherTrustMap map[string]int
}

func NewManager(db *sql.DB, mcpMgr any, pg protocol.PolicyGate, pr protocol.PreferencesRepo, at *substrate.AuditTrail, publisherTrustMap map[string]int) *Manager {
	if publisherTrustMap == nil {
		publisherTrustMap = make(map[string]int)
	}
	return &Manager{
		db:                db,
		mcpMgr:            mcpMgr,
		policyGate:        pg,
		prefsRepo:         pr,
		auditTrail:        at,
		publisherTrustMap: publisherTrustMap,
	}
}

// InstallExtension handles the install flow with M11 Cedar-Gate.
func (m *Manager) InstallExtension(ctx context.Context, req InstallRequest) error {
	mode, err := m.prefsRepo.GetPermissionMode(ctx)
	if err != nil {
		mode = protocol.ModeAutoReview
	}

	// 1. TrustTier Override based on whitelist
	if knownTier, ok := m.publisherTrustMap[req.Publisher]; ok {
		req.TrustTier = knownTier
	} else if req.TrustTier >= int(protocol.TrustOfficial) {
		req.TrustTier = int(protocol.TrustCommunity) // Downgrade self-claimed official
	}

	evalCtx := map[string]any{
		"trust_level":     req.TrustTier,
		"publisher":       req.Publisher,
		"ext_type":        req.ExtType,
		"permission_mode": string(mode),
		"has_hooks":       req.HasHooks,
	}

	reviewReq := protocol.PolicyReviewRequest{
		Principal: req.Principal,
		Action:    "install_extension",
		Resource:  req.ExtensionID,
		Context:   evalCtx,
	}

	result, err := m.policyGate.Review(ctx, reviewReq)
	if err != nil {
		return err
	}

	if result.Allowed {
		// Proceed with installation
		// (simulate DB write to extension_instances with status=installing)
		_, _ = m.db.ExecContext(ctx, "INSERT INTO extension_instances (id, status) VALUES (?, ?)", req.ExtensionID, "installing")
		// Log action (using string literal from substrate.ActionInstallApproved conceptually)
		// Assuming we emit a generic event via eventLogger:
		// m.eventLogger.AppendEvent(...)
		return nil
	}

	if strings.HasPrefix(result.Reason, "forbidden:") {
		return perrors.New(perrors.CodeForbidden, "installation forbidden: "+result.Reason)
	}

	if result.Reason == "denied by default" {
		return ErrRequiresApproval
	}

	return perrors.New(perrors.CodeForbidden, "installation denied")
}
