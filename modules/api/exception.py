class APIException(Exception):
    def __init__(self, status: int = 400, message: str = "API Error"):
        self.status = status
        self.message = message
