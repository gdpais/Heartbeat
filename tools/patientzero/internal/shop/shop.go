// Package shop is PatientZero's business logic and its database,
// patientzero: customers, products and orders. Every entry point runs real
// SQL here, so blocking and slow queries in this database slow the requests
// whose log rows PatientZero writes.
package shop

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Order is one order line as the screens and the API show it.
type Order struct {
	ID        int64     `json:"id"`
	Product   string    `json:"product"`
	Quantity  int       `json:"quantity"`
	Total     float64   `json:"total"`
	CreatedOn time.Time `json:"createdOn"`
}

// Store is what the entry points need from the business database.
type Store interface {
	// Customer returns the customer's id, creating the customer on first use.
	Customer(ctx context.Context, username string) (int, error)
	// Orders returns the customer's newest orders, at most limit.
	Orders(ctx context.Context, customerID, limit int) ([]Order, error)
	// PlaceOrder takes quantity units of the product from stock and records
	// the order, in one transaction.
	PlaceOrder(ctx context.Context, customerID, productID, quantity int) (Order, error)
}

// ErrInvalid is a request the business rules refuse: an unknown product, a
// quantity below one, or not enough stock.
var ErrInvalid = errors.New("invalid order")

// SQLStore is the Store on SQL Server.
type SQLStore struct {
	DB *sql.DB
}

// Schema creates the business tables and seeds the products where they do
// not exist yet. Each statement runs in its own batch; it is safe to run
// again.
var Schema = []string{
	`IF OBJECT_ID(N'dbo.Customer', N'U') IS NULL
CREATE TABLE dbo.Customer (
    Id int IDENTITY(1, 1) NOT NULL CONSTRAINT PK_Customer PRIMARY KEY,
    Username nvarchar(100) NOT NULL CONSTRAINT UQ_Customer_Username UNIQUE,
    CreatedOn datetime2(3) NOT NULL CONSTRAINT DF_Customer_CreatedOn DEFAULT SYSUTCDATETIME()
);`,
	`IF OBJECT_ID(N'dbo.Product', N'U') IS NULL
CREATE TABLE dbo.Product (
    Id int NOT NULL CONSTRAINT PK_Product PRIMARY KEY,
    Name nvarchar(100) NOT NULL,
    Price decimal(10, 2) NOT NULL,
    Stock int NOT NULL
);`,
	`IF OBJECT_ID(N'dbo.SalesOrder', N'U') IS NULL
CREATE TABLE dbo.SalesOrder (
    Id bigint IDENTITY(1, 1) NOT NULL CONSTRAINT PK_SalesOrder PRIMARY KEY,
    CustomerId int NOT NULL CONSTRAINT FK_SalesOrder_Customer REFERENCES dbo.Customer (Id),
    ProductId int NOT NULL CONSTRAINT FK_SalesOrder_Product REFERENCES dbo.Product (Id),
    Quantity int NOT NULL,
    Total decimal(12, 2) NOT NULL,
    CreatedOn datetime2(3) NOT NULL CONSTRAINT DF_SalesOrder_CreatedOn DEFAULT SYSUTCDATETIME()
);`,
	`IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_SalesOrder_CustomerId' AND object_id = OBJECT_ID(N'dbo.SalesOrder'))
CREATE INDEX IX_SalesOrder_CustomerId ON dbo.SalesOrder (CustomerId, Id DESC);`,
	`INSERT INTO dbo.Product (Id, Name, Price, Stock)
SELECT s.Id, s.Name, s.Price, s.Stock
FROM (VALUES
    (1, N'Thermometer', 12.50, 1000000000),
    (2, N'Stethoscope', 89.90, 1000000000),
    (3, N'Bandage roll', 3.20, 1000000000),
    (4, N'Face mask (50)', 9.99, 1000000000),
    (5, N'Hand sanitizer', 4.75, 1000000000)
) AS s (Id, Name, Price, Stock)
WHERE NOT EXISTS (SELECT 1 FROM dbo.Product AS p WHERE p.Id = s.Id);`,
}

// Customer upserts in one statement; the key-range lock keeps two first
// requests of the same user from both inserting.
func (s *SQLStore) Customer(ctx context.Context, username string) (int, error) {
	var id int
	err := s.DB.QueryRowContext(ctx, `
SET NOCOUNT ON;
INSERT INTO dbo.Customer (Username)
SELECT @username
WHERE NOT EXISTS (SELECT 1 FROM dbo.Customer WITH (UPDLOCK, HOLDLOCK) WHERE Username = @username);
SELECT Id FROM dbo.Customer WHERE Username = @username;`,
		sql.Named("username", username)).Scan(&id)
	return id, err
}

func (s *SQLStore) Orders(ctx context.Context, customerID, limit int) ([]Order, error) {
	rows, err := s.DB.QueryContext(ctx, `
SELECT TOP (@limit) o.Id, p.Name, o.Quantity, o.Total, o.CreatedOn
FROM dbo.SalesOrder AS o
JOIN dbo.Product AS p ON p.Id = o.ProductId
WHERE o.CustomerId = @customer
ORDER BY o.Id DESC;`, sql.Named("limit", limit), sql.Named("customer", customerID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := []Order{}
	for rows.Next() {
		var o Order
		if err := rows.Scan(&o.ID, &o.Product, &o.Quantity, &o.Total, &o.CreatedOn); err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}

func (s *SQLStore) PlaceOrder(ctx context.Context, customerID, productID, quantity int) (Order, error) {
	if quantity < 1 {
		return Order{}, fmt.Errorf("%w: quantity %d is below 1", ErrInvalid, quantity)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Order{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var o Order
	err = tx.QueryRowContext(ctx, `
SET NOCOUNT ON;
DECLARE @ordered TABLE (Id bigint, Total decimal(12, 2), CreatedOn datetime2(3));
UPDATE dbo.Product SET Stock = Stock - @quantity
WHERE Id = @product AND Stock >= @quantity;
IF @@ROWCOUNT = 1
    INSERT INTO dbo.SalesOrder (CustomerId, ProductId, Quantity, Total)
    OUTPUT inserted.Id, inserted.Total, inserted.CreatedOn INTO @ordered
    SELECT @customer, Id, @quantity, Price * @quantity FROM dbo.Product WHERE Id = @product;
SELECT o.Id, p.Name, o.Total, o.CreatedOn FROM @ordered AS o CROSS JOIN dbo.Product AS p WHERE p.Id = @product;`,
		sql.Named("customer", customerID), sql.Named("product", productID), sql.Named("quantity", quantity),
	).Scan(&o.ID, &o.Product, &o.Total, &o.CreatedOn)
	if errors.Is(err, sql.ErrNoRows) {
		return Order{}, fmt.Errorf("%w: product %d does not exist or has fewer than %d in stock", ErrInvalid, productID, quantity)
	}
	if err != nil {
		return Order{}, err
	}
	o.Quantity = quantity
	return o, tx.Commit()
}
