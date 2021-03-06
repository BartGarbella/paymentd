package payment

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"code.google.com/p/godec/dec"
	"github.com/fritzpay/paymentd/pkg/decimal"
)

type PaymentTransactionStatus string

// Scan implements the Scanner interface for sql
func (s *PaymentTransactionStatus) Scan(v interface{}) error {
	switch src := v.(type) {
	case []byte:
		*s = PaymentTransactionStatus(string(src))
		return nil
	case nil:
		*s = PaymentStatusNone
		return nil
	}
	str, ok := v.(string)
	if !ok {
		return fmt.Errorf("cannot scan %T into %T", v, s)
	}
	*s = PaymentTransactionStatus(str)
	return nil
}

// Value implements the Valuer interface for sql
func (s PaymentTransactionStatus) Value() (driver.Value, error) {
	return driver.Value(s.String()), nil
}

func (s PaymentTransactionStatus) Valid() bool {
	return string(s) != ""
}

func (s PaymentTransactionStatus) String() string {
	return string(s)
}

const (
	PaymentStatusNone           PaymentTransactionStatus = "uninitialized"
	PaymentStatusOpen                                    = "open"
	PaymentStatusPending                                 = "pending"
	PaymentStatusPaid                                    = "paid"
	PaymentStatusSettled                                 = "settled"
	PaymentStatusAuthorized                              = "authorized"
	PaymentStatusError                                   = "error"
	PaymentStatusCancelled                               = "cancelled"
	PaymentStatusFailed                                  = "failed"
	PaymentStatusChargeback                              = "chargeback"
	PaymentStatusRefunded                                = "refunded"
	PaymentStatusRefundReversed                          = "refund-reversed"
)

// PaymentTransaction represents a transaction on a payment
//
// A transaction is any event/status change on a payment
//
// The transactions represents the ledger on a payment
type PaymentTransaction struct {
	Payment *Payment

	Timestamp time.Time
	Amount    int64
	Subunits  int8
	Currency  string
	Status    PaymentTransactionStatus
	Comment   sql.NullString
}

func (p *PaymentTransaction) Decimal() *decimal.Decimal {
	d := dec.NewDecInt64(p.Amount)
	sc := dec.Scale(int32(p.Subunits))
	d.SetScale(sc)
	return &decimal.Decimal{Dec: *d}
}

// Balance represents a balance which totals the ledger by currency
type Balance map[string]*decimal.Decimal

func (b Balance) FlatMap() map[string]string {
	flat := make(map[string]string)
	for curr, dec := range b {
		flat[curr] = dec.String()
	}
	return flat
}

func (b Balance) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.FlatMap())
}

func (b *Balance) UnmarshalJSON(p []byte) error {
	m := make(map[string]string)
	err := json.Unmarshal(p, &m)
	if err != nil {
		return err
	}
	bal := make(map[string]*decimal.Decimal)
	for k, v := range m {
		dec := dec.NewDecInt64(0)
		_, ok := dec.SetString(v)
		if !ok {
			return fmt.Errorf("error decoding decimal %s", v)
		}
		bal[k] = &decimal.Decimal{Dec: *dec}
	}
	*b = Balance(bal)
	return nil
}

type PaymentTransactionList []*PaymentTransaction

func (p PaymentTransactionList) Balance() Balance {
	b := make(map[string]*decimal.Decimal)
	for _, tx := range p {
		am := tx.Decimal()
		if _, ok := b[tx.Currency]; ok {
			b[tx.Currency].Add(&b[tx.Currency].Dec, &am.Dec)
		} else {
			b[tx.Currency] = am
		}
	}
	return b
}
