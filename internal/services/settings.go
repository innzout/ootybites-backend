package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/innzout/ootybites/internal/db"
	"github.com/innzout/ootybites/internal/models"
)

// Settings is the store-configuration service (single-row `settings` table).
type Settings struct {
	db *db.Pool
}

// NewSettings builds the settings service.
func NewSettings(pool *db.Pool) *Settings { return &Settings{db: pool} }

const settingsCols = `store_name, tagline, support_email, support_phone, store_address,
	standard_delivery_text, express_delivery_text, cod_note, order_number_prefix, updated_at`

func scanSettings(row interface {
	Scan(dest ...any) error
}) (*models.Settings, error) {
	var s models.Settings
	if err := row.Scan(&s.StoreName, &s.Tagline, &s.SupportEmail, &s.SupportPhone, &s.StoreAddress,
		&s.StandardDeliveryText, &s.ExpressDeliveryText, &s.CODNote, &s.OrderNumberPrefix, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// Get returns the store settings row (seeded by migration, so always present).
func (s *Settings) Get(ctx context.Context) (*models.Settings, error) {
	out, err := scanSettings(s.db.QueryRow(ctx, `SELECT `+settingsCols+` FROM settings WHERE id=1`))
	if err != nil {
		return nil, fmt.Errorf("get settings: %w", err)
	}
	return out, nil
}

// Update writes the editable settings fields and returns the fresh row. A blank
// order-number prefix falls back to "OB" so order numbers always have a prefix.
func (s *Settings) Update(ctx context.Context, in *models.Settings) (*models.Settings, error) {
	prefix := strings.TrimSpace(in.OrderNumberPrefix)
	if prefix == "" {
		prefix = "OB"
	}
	out, err := scanSettings(s.db.QueryRow(ctx,
		`UPDATE settings SET
		   store_name=$1, tagline=$2, support_email=$3, support_phone=$4, store_address=$5,
		   standard_delivery_text=$6, express_delivery_text=$7, cod_note=$8, order_number_prefix=$9,
		   updated_at=now()
		 WHERE id=1
		 RETURNING `+settingsCols,
		strings.TrimSpace(in.StoreName), strings.TrimSpace(in.Tagline), strings.TrimSpace(in.SupportEmail),
		strings.TrimSpace(in.SupportPhone), strings.TrimSpace(in.StoreAddress), strings.TrimSpace(in.StandardDeliveryText),
		strings.TrimSpace(in.ExpressDeliveryText), strings.TrimSpace(in.CODNote), prefix))
	if err != nil {
		return nil, fmt.Errorf("update settings: %w", err)
	}
	return out, nil
}
