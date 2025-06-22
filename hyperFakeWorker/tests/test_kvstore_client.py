import pytest

from worker.kvstore.client import KVStoreClient

@pytest.mark.external
def test_client():
    c = KVStoreClient("127.0.0.1:8999")
    image = c.get_image("2e695271-8be9-4cdb-beb9-7480451753ce")