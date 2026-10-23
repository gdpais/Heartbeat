package shop

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestWaitforTime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		1500 * time.Millisecond:                     "00:00:01.500",
		3*time.Second + 999*time.Microsecond:        "00:00:03.000",
		time.Minute:                                 "00:01:00.000",
		2*time.Hour + 3*time.Minute + 4*time.Second: "02:03:04.000",
		48 * time.Hour:                              "23:59:59.999",
	} {
		if got := waitforTime(d); got != want {
			t.Errorf("waitforTime(%s) = %s, want %s", d, got, want)
		}
	}
}

// A delay in the context puts a WAITFOR in front of the statement, in the
// same batch, so SQL Server runs the request for that long; none leaves the
// statement as it is.
func TestWithDelay(t *testing.T) {
	q, args := withDelay(context.Background(), ordersSQL, sql.Named("limit", 20))
	if q != ordersSQL || len(args) != 1 {
		t.Fatalf("no delay: %q %v", q, args)
	}
	ctx := WithDelay(context.Background(), 2500*time.Millisecond)
	if Delay(ctx) != 2500*time.Millisecond || Delay(context.Background()) != 0 {
		t.Fatal("Delay does not read back what WithDelay set")
	}
	q, args = withDelay(ctx, ordersSQL, sql.Named("limit", 20))
	if !strings.HasPrefix(q, "WAITFOR DELAY @pz_delay;\n") || !strings.HasSuffix(q, ordersSQL) {
		t.Fatalf("delayed query %q", q)
	}
	last := args[len(args)-1].(sql.NamedArg)
	if last.Name != "pz_delay" || last.Value != "00:00:02.500" {
		t.Fatalf("delay argument %+v", last)
	}
	if WithDelay(context.Background(), 0) != context.Background() {
		t.Fatal("a zero delay must leave the context alone")
	}
}

func TestStatementsAreOneLine(t *testing.T) {
	for name, want := range map[string]string{
		"Orders":     "SELECT TOP (@limit) o.Id, p.Name, o.Quantity, o.Total, o.CreatedOn FROM dbo.SalesOrder AS o JOIN dbo.Product AS p ON p.Id = o.ProductId WHERE o.CustomerId = @customer ORDER BY o.Id DESC",
		"PlaceOrder": "UPDATE dbo.Product SET Stock = Stock - @quantity WHERE Id = @product AND Stock >= @quantity",
	} {
		if got := Statements[name]; got != want {
			t.Errorf("Statements[%s] = %q, want %q", name, got, want)
		}
	}
}
