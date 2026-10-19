// The traffic PatientZero's load generates at steady load: who visits, what
// they do and how often. These are stand-in numbers until the
// logging-database survey [3.3] gives the replica's aggregates (rows per
// minute per log type, error ratio, identifier coverage); only this file
// changes then.
export const traffic = {
  // Browser users of the Traditional screen and the Reactive app, one per
  // virtual user, each with a stable username and client IP.
  shoppers: {
    users: 20,
    // Share of sessions that browse without signing in (user 0). Anonymous
    // sessions only open screens: placing an order needs a customer.
    anonymousShare: 0.1,
    // Screens and server actions in one session, inclusive.
    stepsPerSession: [2, 6],
    // Share of a signed-in session's steps, after its first screen, that
    // place an order instead of opening the order list again.
    orderShare: 0.4,
    // Share of orders the business rules refuse (an unknown product): HTTP
    // 400 and an error row under the same Request_Key.
    invalidOrderShare: 0.03,
    // Seconds between two steps of a session, and between two sessions of
    // the same user, inclusive.
    thinkSeconds: [1, 5],
    pauseSeconds: [5, 20],
  },
  // Partner systems calling the exposed REST API, without a session.
  apiClients: {
    clients: 3,
    requestsPerMinute: 60,
  },
  // Product ids and quantities a valid order picks from.
  products: [1, 2, 3, 4, 5],
  quantity: [1, 3],
  // The peak profile runs this many times the steady users and API rate.
  peakFactor: 3,
};
