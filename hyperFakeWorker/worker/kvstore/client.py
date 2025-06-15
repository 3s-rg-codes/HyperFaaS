import httpx

from ..function import FunctionIdStr
from ..function.image import FunctionImage

class KVStoreClient():

    def __init__(self, address: str, port: int = None):
        if port is None:
            address, port = address.split(":")
        self.address = address
        self.port = int(port)

    def get_image_info(self, function_id: FunctionIdStr):
        response = httpx.request(
            url=f"http://{self.address}:{self.port}/",
            method="GET",
            content=function_id
        )
        return response
    
    def get_image(self, function_id: FunctionIdStr) -> FunctionImage:
        response=self.get_image_info(function_id)
        data = response.json()
        raw_image_tag = data["image_tag"]
        return FunctionImage.from_str(raw_image_tag)