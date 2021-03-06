package fritzpay

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/fritzpay/paymentd/pkg/paymentd/payment"
	"github.com/fritzpay/paymentd/pkg/paymentd/payment_method"
	paymentService "github.com/fritzpay/paymentd/pkg/service/payment"
	"github.com/go-sql-driver/mysql"
	"golang.org/x/net/context"
	"gopkg.in/inconshreveable/log15.v2"
)

func (d *Driver) InitPayment(p *payment.Payment, method *payment_method.Method) (http.Handler, error) {
	log := d.log.New(log15.Ctx{
		"method":          "InitPayment",
		"projectID":       p.ProjectID(),
		"paymentID":       p.ID(),
		"paymentMethodID": method.ID,
	})
	if Debug {
		log.Debug("initialize payment")
	}
	if method.Disabled() {
		log.Warn("payment requested with disabled payment method")
		return nil, fmt.Errorf("disabled payment method id %d", method.ID)
	}

	var tx *sql.Tx
	var commit bool
	var err error
	defer func() {
		if tx != nil && !commit {
			err = tx.Rollback()
			if err != nil {
				log.Crit("error on rollback", log15.Ctx{"err": err})
			}
		}
	}()
	maxRetries := d.ctx.Config().Database.TransactionMaxRetries
	var retries int
beginTx:
	if retries >= maxRetries {
		// no need to roll back
		commit = true
		log.Crit("too many retries on tx. aborting...", log15.Ctx{"maxRetries": maxRetries})
		return nil, ErrDB
	}
	tx, err = d.ctx.PaymentDB().Begin()
	if err != nil {
		commit = true
		log.Crit("error on begin tx", log15.Ctx{"err": err})
		return nil, ErrDB
	}
	fritzpayP, err := PaymentByPaymentIDTx(tx, p.PaymentID())
	if err != nil && err != ErrPaymentNotFound {
		log.Error("error retrieving payment id", log15.Ctx{"err": err})
		return nil, ErrDB
	}
	// payment does already exist
	if err == nil {
		if fritzpayP.MethodKey != method.MethodKey {
			log.Crit("payment does exist but has a different method key", log15.Ctx{
				"registeredMethodKey": fritzpayP.MethodKey,
				"requestMethodKey":    method.MethodKey,
			})
			return nil, ErrConflict
		}
	}
	if err == ErrPaymentNotFound {
		// create new fritzpay payment
		fritzpayP.ProjectID = p.ProjectID()
		fritzpayP.PaymentID = p.ID()
		fritzpayP.Created = time.Now()
		fritzpayP.MethodKey = method.MethodKey
		err = InsertPaymentTx(tx, &fritzpayP)
		if err != nil {
			log.Error("error creating new payment", log15.Ctx{"err": err})
			return nil, ErrDB
		}
	}
	log = log.New(log15.Ctx{"fritzpayPaymentID": fritzpayP.ID})

	if currentStatus, err := d.paymentService.PaymentTransaction(tx, p); err != nil && err != payment.ErrPaymentTransactionNotFound {
		log.Error("error retrieving payment transaction", log15.Ctx{"err": err})
		return nil, ErrDB
	} else {
		if currentStatus.Status != payment.PaymentStatusPending {
			paymentTx := p.NewTransaction(payment.PaymentStatusPending)
			paymentTx.Amount = 0
			paymentTx.Comment.String, paymentTx.Comment.Valid = "initialized by FritzPay demo provider", true
			err = d.paymentService.SetPaymentTransaction(tx, paymentTx)
			if err != nil {
				if err == paymentService.ErrDBLockTimeout {
					retries++
					time.Sleep(time.Second)
					goto beginTx
				}
				log.Error("error setting payment tx", log15.Ctx{"err": err})
				return nil, ErrDB
			}
		}
	}
	err = tx.Commit()
	if err != nil {
		if mysqlErr, ok := err.(*mysql.MySQLError); ok {
			if mysqlErr.Number == 1213 {
				retries++
				time.Sleep(time.Second)
				goto beginTx
			}
		}
		log.Crit("error on commit", log15.Ctx{"err": err})
		commit = true
		return nil, ErrDB
	}
	commit = true

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// call psp init worker
		// this simulates the initialization of the payment on the
		// payment service provider (PSP) end
		//
		// the worker will call the web server (pretty much itself)
		routeURL, err := d.mux.GetRoute("fritzpayCallback").URL()
		if err != nil {
			log.Error("error retrieving callback URL", log15.Ctx{"err": err})
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		callbackURL, err := url.Parse(d.ctx.Config().Web.URL)
		if err != nil {
			log.Error("error parsing web URL", log15.Ctx{"err": err})
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		callbackURL.Path = routeURL.Path
		q := callbackURL.Query()
		q.Set("paymentID", d.paymentService.EncodedPaymentID(p.PaymentID()).String())
		callbackURL.RawQuery = q.Encode()

		workerCtx, _ := context.WithTimeout(d.ctx, fritzpayDefaultTimeout)
		go pspInit(workerCtx, fritzpayP, callbackURL.String())
		defer func() {
			if err := recover(); err != nil {
				log.Crit("panic on worker", log15.Ctx{"err": err})
			}
		}()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}), nil
}
