package scale

import (
	"fmt"
	"math"
	"math/rand/v2"
	"strings"
)

// module is one OutSystems module (eSpace) and what runs in it. Names are
// letters only: the collector's error fingerprint replaces digits, so a
// digit would merge error kinds that differ only in it.
type module struct {
	ID          int
	Name        string
	Application string
	AppKey      string
	Screens     []string
	Actions     []string
}

// applications are the installation's applications and their modules.
var applications = []struct {
	name    string
	modules []string
}{
	{"Orders", []string{"Orders_CS", "Orders_Web", "Orders_IS"}},
	{"Billing", []string{"Billing_CS", "Billing_Web"}},
	{"Customers", []string{"Customers_CS", "Customers_Web"}},
	{"Catalog", []string{"Catalog_CS", "Catalog_Web"}},
	{"Shipping", []string{"Shipping_CS", "Shipping_Web", "Shipping_IS"}},
	{"Backoffice", []string{"Backoffice_Web", "Reports_Web"}},
}

var (
	screenNames = []string{"Home", "List", "Detail", "Edit", "Search", "Dashboard", "Approve", "History"}
	actionNames = []string{"Preparation", "Save", "Submit", "Refresh", "Filter", "Export", "Approve", "Cancel"}
	// Error-raising actions, combined with modules and exceptions into
	// distinct error kinds.
	errorActions = []string{"CreateOrder", "UpdateOrder", "CancelOrder", "SyncStock", "ValidateAddress",
		"ExportInvoices", "ImportPrices", "ApproveRefund"}
	extensionNames = []string{"PdfGenerator", "ExcelExport", "CryptoAPI", "FileStorage", "SmtpRelay"}
	extensionActs  = []string{"Render", "Convert", "Encrypt", "Upload", "Send", "Read"}
	partners       = []string{"payments", "erp", "addresses", "carrier", "crm", "tax"}
	entities       = []string{"Order", "OrderLine", "Invoice", "Customer", "Product", "Shipment", "Price"}
	timerNames     = []string{"SyncStock", "SendReminders", "CloseOrders", "PurgeCarts", "ImportPrices",
		"RebuildIndex", "ExportLedger", "RetryPayments", "ArchiveLogs", "RefreshRates"}
	frontEnds = []string{"FE-01", "FE-02", "FE-03", "FE-04"}
)

// exceptions are the error messages kinds are built from. %d and %s are
// filled per occurrence with numbers and quoted values the fingerprint
// ignores.
var exceptions = []struct {
	message string
	long    bool // raised by the database provider: a long trace
}{
	{"Timeout expired. The timeout period elapsed prior to completion of the operation or the server is not responding (order %d).", true},
	{"Transaction (Process ID %d) was deadlocked on lock resources with another process and has been chosen as the deadlock victim. Rerun the transaction.", true},
	{"Violation of PRIMARY KEY constraint 'PK_%s'. Cannot insert duplicate key in object 'dbo.OSUSR_%s'.", true},
	{"Object reference not set to an instance of an object.", false},
	{"The remote server returned an error: (%d) Service Unavailable.", false},
	{"Invalid value '%s' for parameter Quantity.", false},
	{"Access denied for user %d.", false},
	{"The operation has timed out after %d ms waiting for the partner.", false},
}

// dataProviderFrames are a database failure's innermost frames: with them a
// trace runs past the error cycle table's 2000 characters.
var dataProviderFrames = []string{
	"at System.Data.SqlClient.SqlConnection.OnError(SqlException exception, Boolean breakConnection, Action`1 wrapCloseInAction)",
	"at System.Data.SqlClient.SqlInternalConnection.OnError(SqlException exception, Boolean breakConnection, Action`1 wrapCloseInAction)",
	"at System.Data.SqlClient.TdsParser.ThrowExceptionAndWarning(TdsParserStateObject stateObj, Boolean callerHasConnectionLock, Boolean asyncClose)",
	"at System.Data.SqlClient.TdsParser.TryRun(RunBehavior runBehavior, SqlCommand cmdHandler, SqlDataReader dataStream, BulkCopySimpleResultSet bulkCopyHandler, TdsParserStateObject stateObj, Boolean& dataReady)",
	"at System.Data.SqlClient.SqlDataReader.TryConsumeMetaData()",
	"at System.Data.SqlClient.SqlDataReader.get_MetaData()",
	"at System.Data.SqlClient.SqlCommand.FinishExecuteReader(SqlDataReader ds, RunBehavior runBehavior, String resetOptionsString, Boolean isInternal, Boolean forDescribeParameterEncryption, Boolean shouldCacheForAlwaysEncrypted)",
	"at System.Data.SqlClient.SqlCommand.RunExecuteReaderTds(CommandBehavior cmdBehavior, RunBehavior runBehavior, Boolean returnStream, Boolean async, Int32 timeout, Task& task, Boolean asyncWrite, Boolean inRetry, SqlDataReader ds, Boolean describeParameterEncryptionRequest)",
	"at System.Data.SqlClient.SqlCommand.RunExecuteReader(CommandBehavior cmdBehavior, RunBehavior runBehavior, Boolean returnStream, String method, TaskCompletionSource`1 completion, Int32 timeout, Task& task, Boolean& usedCache, Boolean asyncWrite, Boolean inRetry)",
	"at System.Data.SqlClient.SqlCommand.ExecuteReader(CommandBehavior behavior, String method)",
	"at System.Data.Common.DbCommand.System.Data.IDbCommand.ExecuteReader(CommandBehavior behavior)",
	"at System.Data.Common.DbDataAdapter.FillInternal(DataSet dataset, DataTable[] datatables, Int32 startRecord, Int32 maxRecords, String srcTable, IDbCommand command, CommandBehavior behavior)",
	"at System.Data.Common.DbDataAdapter.Fill(DataSet dataSet, Int32 startRecord, Int32 maxRecords, String srcTable, IDbCommand command, CommandBehavior behavior)",
	"at System.Data.Common.DbDataAdapter.Fill(DataSet dataSet)",
}

// modules returns the installation's modules, in a fixed order.
func modules() []module {
	var out []module
	id := 100
	for a, app := range applications {
		for _, name := range app.modules {
			id++
			out = append(out, module{
				ID: id, Name: name, Application: app.name,
				AppKey:  fmt.Sprintf("0a7e1e57-0000-4000-8000-%012x", a+1),
				Screens: screenNames, Actions: actionNames,
			})
		}
	}
	return out
}

// errorKind is one distinct error: one fingerprint.
type errorKind struct {
	Name    string // module.action: exception number, for the expected answers
	module  module
	action  string
	message string
	frames  []string
	weight  float64
}

// Long reports whether the kind's trace goes to the detail table.
func (k *errorKind) Long() bool { return len(k.frames) > 2 }

// occurrence is the kind's message and stack for one error: numbers and
// quoted values vary, the rest does not.
func (k *errorKind) occurrence(r *rand.Rand) (message, stack string) {
	var args []any
	for i := 0; i+1 < len(k.message); i++ {
		if k.message[i] != '%' {
			continue
		}
		switch k.message[i+1] {
		case 'd':
			args = append(args, r.IntN(100000))
		case 's':
			args = append(args, entities[r.IntN(len(entities))]+fmt.Sprint(r.IntN(1000)))
		}
	}
	return fmt.Sprintf(k.message, args...), strings.Join(k.frames, "\r\n")
}

// errorKinds returns n distinct kinds, in a seeded order: every module,
// action and exception combination is a different fingerprint, since the
// module and action are the stack's first frame. About longShare of them
// are database failures with a long trace.
func errorKinds(r *rand.Rand, mods []module, n int, longShare float64) []*errorKind {
	type combo struct{ m, a, e int }
	var long, short []combo
	for m := range mods {
		for a := range errorActions {
			for e, ex := range exceptions {
				if ex.long {
					long = append(long, combo{m, a, e})
				} else {
					short = append(short, combo{m, a, e})
				}
			}
		}
	}
	r.Shuffle(len(long), func(i, j int) { long[i], long[j] = long[j], long[i] })
	r.Shuffle(len(short), func(i, j int) { short[i], short[j] = short[j], short[i] })
	var out []*errorKind
	for i := range n {
		// Long kinds are spread evenly, so any prefix keeps the share.
		pick := &short
		if len(long) > 0 && (countLong(out) < int(math.Round(float64(i+1)*longShare)) || len(short) == 0) {
			pick = &long
		}
		if len(*pick) == 0 {
			break
		}
		c := (*pick)[0]
		*pick = (*pick)[1:]
		m, action, ex := mods[c.m], errorActions[c.a], exceptions[c.e]
		frames := []string{
			fmt.Sprintf("at ss%s.Actions.Action%s(HeContext heContext)", strings.ReplaceAll(m.Name, "_", ""), action),
			fmt.Sprintf("at ss%s.Flows.FlowMain.Scr%s.Preparation(HeContext heContext)", strings.ReplaceAll(m.Name, "_", ""), m.Screens[c.a%len(m.Screens)]),
		}
		if ex.long {
			frames = append(frames, dataProviderFrames...)
		}
		out = append(out, &errorKind{
			Name:    fmt.Sprintf("%s.%s: %s", m.Name, action, strings.SplitN(ex.message, ".", 2)[0]),
			module:  m,
			action:  action,
			message: ex.message,
			frames:  frames,
			// A long tail: a few kinds make most errors.
			weight: 1 / float64(i+1),
		})
	}
	return out
}

func countLong(kinds []*errorKind) int {
	n := 0
	for _, k := range kinds {
		if k.Long() {
			n++
		}
	}
	return n
}
