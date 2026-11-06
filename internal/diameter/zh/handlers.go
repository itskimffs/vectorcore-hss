// Modified by itskimffs (2026-11), see NOTICE: Zh resynchronisation and GUSS.
// This file is part of vectorcore-hss, licensed under the Apache License,
// Version 2.0 (see LICENSE).

package zh

import (
	"github.com/svinson1121/vectorcore-hss/internal/config"
	"github.com/svinson1121/vectorcore-hss/internal/repository"
	"go.uber.org/zap"
)

type Handlers struct {
	store       repository.Repository
	log         *zap.Logger
	originHost  string
	originRealm string
	mcc, mnc    string
}

func NewHandlers(cfg *config.Config, store repository.Repository, log *zap.Logger) *Handlers {
	return &Handlers{
		store:       store,
		log:         log,
		originHost:  cfg.HSS.OriginHost,
		originRealm: cfg.HSS.OriginRealm,
		mcc:         cfg.HSS.MCC,
		mnc:         cfg.HSS.MNC,
	}
}
