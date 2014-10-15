package api

import (
	"github.com/fritzpay/paymentd/pkg/service"
	"github.com/fritzpay/paymentd/pkg/service/api/v1"
	"gopkg.in/inconshreveable/log15.v2"
	"net/http"
)

// Handler is the (HTTP) API Handler
type Handler struct {
	ctx *service.Context
	log log15.Logger

	mux *http.ServeMux
}

// NewHandler creates a new API Handler
func NewHandler(ctx *service.Context) (*Handler, error) {
	h := &Handler{
		ctx: ctx,
		log: ctx.Log().New(log15.Ctx{
			"pkg": "github.com/fritzpay/paymentd/pkg/service/api",
		}),

		mux: http.NewServeMux(),
	}

	h.log.Info("registering API service v1...")
	v1.NewService(h.ctx, h.mux)

	return h, nil
}

// ServeHTTP implements the http.Handler
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := recover(); err != nil {
			h.log.Crit("panic on serving HTTP", log15.Ctx{"panic": err})
		}
	}()
	service.SetRequestContext(r, h.ctx)
	defer service.Clear(r)
	h.mux.ServeHTTP(w, r)
}