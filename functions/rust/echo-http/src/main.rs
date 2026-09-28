use std::sync::atomic::{AtomicBool, Ordering};

use http_body_util::BodyExt;
use hyper::{Request, Response, body::Incoming};

static IS_COLD: AtomicBool = AtomicBool::new(true);

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    hyperfaas_function_runtime::ready(echo).await
}

async fn echo(req: Request<Incoming>) -> Result<Response<String>, hyper::Error> {
    let body = req.into_body().collect().await?.to_bytes();
    let cold = IS_COLD.swap(false, Ordering::SeqCst);
    let mut response = Response::new(String::from_utf8_lossy(&body).into_owned());
    response.headers_mut().insert("X-Cold", if cold { "1" } else { "0" }.parse().unwrap());
    Ok(response)
}
