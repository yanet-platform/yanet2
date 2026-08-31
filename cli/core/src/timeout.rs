//! Tower middleware bounding one request to a fixed time budget.

use core::{
    error::Error,
    future::Future,
    pin::Pin,
    task::{Context, Poll},
    time::Duration,
};

use tonic::Status;
use tower::{Layer, Service};

type BoxError = Box<dyn Error + Send + Sync>;

/// Tower layer that wraps a service with a per-request time budget.
///
/// `None` leaves the wrapped service untouched.
#[derive(Clone, Copy)]
pub struct TimeoutLayer {
    budget: Option<Duration>,
}

impl TimeoutLayer {
    /// Create a layer enforcing `budget` on every request, or none at all.
    pub fn new(budget: Option<Duration>) -> Self {
        Self { budget }
    }
}

impl<S> Layer<S> for TimeoutLayer {
    type Service = TimeoutService<S>;

    fn layer(&self, inner: S) -> Self::Service {
        TimeoutService { inner, budget: self.budget }
    }
}

/// Tower service that fails a request still pending after its time budget.
#[derive(Clone)]
pub struct TimeoutService<S> {
    inner: S,
    budget: Option<Duration>,
}

impl<S, B> Service<http::Request<B>> for TimeoutService<S>
where
    S: Service<http::Request<B>> + Clone + Send + 'static,
    S::Future: Send,
    S::Response: Send,
    S::Error: Into<BoxError> + Send,
    B: Send + 'static,
{
    type Response = S::Response;
    type Error = BoxError;
    type Future = Pin<Box<dyn Future<Output = Result<Self::Response, Self::Error>> + Send>>;

    fn poll_ready(&mut self, cx: &mut Context<'_>) -> Poll<Result<(), Self::Error>> {
        self.inner.poll_ready(cx).map_err(Into::into)
    }

    fn call(&mut self, request: http::Request<B>) -> Self::Future {
        // Standard Tower pattern: clone and swap to get an owned service
        // for the async block.
        let clone = self.inner.clone();
        let mut inner = core::mem::replace(&mut self.inner, clone);
        let budget = self.budget;

        Box::pin(async move {
            let Some(budget) = budget else {
                return inner.call(request).await.map_err(Into::into);
            };

            tokio::select! {
                result = inner.call(request) => result.map_err(Into::into),
                () = tokio::time::sleep(budget) => {
                    let message = format!("request timed out after {}s", budget.as_secs_f64());
                    Err(Box::new(Status::unavailable(message)) as BoxError)
                }
            }
        })
    }
}

#[cfg(test)]
mod test {
    use core::{convert::Infallible, time::Duration};

    use tonic::{Code, Status};
    use tower::{service_fn, Layer, Service, ServiceExt};

    use super::TimeoutLayer;

    /// Verifies that a service still pending after its budget fails with an
    /// `Unavailable` status naming the budget.
    #[tokio::test(start_paused = true)]
    async fn test_call_pending_past_budget_reports_unavailable() {
        let inner =
            service_fn(|_: http::Request<()>| core::future::pending::<Result<http::Response<()>, Infallible>>());
        let mut service = TimeoutLayer::new(Some(Duration::from_secs(1))).layer(inner);

        let err = service
            .ready()
            .await
            .unwrap()
            .call(http::Request::new(()))
            .await
            .unwrap_err();

        let status = err.downcast_ref::<Status>().expect("error is a tonic Status");

        assert_eq!(Code::Unavailable, status.code());
        assert_eq!("request timed out after 1s", status.message());
    }

    /// Verifies that a `None` budget passes a ready response through
    /// unchanged.
    #[tokio::test]
    async fn test_call_without_budget_passes_response_through() {
        let inner = service_fn(|_: http::Request<()>| async { Ok::<_, Infallible>(http::Response::new(())) });
        let mut service = TimeoutLayer::new(None).layer(inner);

        let response = service
            .ready()
            .await
            .unwrap()
            .call(http::Request::new(()))
            .await
            .unwrap();

        assert_eq!((), *response.body());
    }
}
