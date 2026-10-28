// The shapes of Carteiro's API responses (README, "API"). Times are RFC 3339
// strings, as they arrive in JSON.

export type Application = {
  id: string;
  name: string;
  created_at: string;
};

export type EndpointStatus = "active" | "disabled";

export type Endpoint = {
  id: string;
  url: string;
  description: string;
  event_types: string[];
  status: EndpointStatus;
  // "gone" (the receiver answered 410) or "failing" (FR-08).
  disabled_reason: string | null;
  consecutive_failures: number;
  failing_since: string | null;
  created_at: string;
  updated_at: string;
  // Only in the responses that create the endpoint or rotate its secret.
  secret?: string;
};

export type Event = {
  id: string;
  event_type: string;
  payload: unknown;
  idempotency_key: string | null;
  created_at: string;
};

export type DeliveryStatus = "pending" | "in_flight" | "succeeded" | "dead";

export type Delivery = {
  id: string;
  event_id: string;
  endpoint_id: string;
  status: DeliveryStatus;
  attempt_count: number;
  next_attempt_at: string | null;
  created_at: string;
  updated_at: string;
};

export type Attempt = {
  id: string;
  attempt_number: number;
  started_at: string;
  duration_ms: number;
  // Null when no response arrived: a timeout, a refused connection, or a
  // blocked address.
  response_status: number | null;
  response_body: string | null;
  error: string | null;
};

export type Page<T> = {
  data: T[];
  next_cursor: string | null;
};

// RFC 9457 problem details, as every error is returned.
export type Problem = {
  type: string;
  title: string;
  status: number;
  detail?: string;
  request_id?: string;
  errors?: { field: string; message: string }[];
};
