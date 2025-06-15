
class FunctionImage():

    def __init__(self, image: str, tag: str):
        self.image: str = image
        self.tag = tag

    @staticmethod
    def from_str(image_string: str):
        parts = image_string.split(":")
        if len(parts) > 2 or len(parts) < 1:
            raise RuntimeError("An image should consist of the image name and the tag!")
        elif len(parts) == 2:
            return FunctionImage(parts[0], parts[1])
        else:
            return FunctionImage(parts[0], "")
        
    def __str__(self):
        return f"{self.image}:{self.tag}"