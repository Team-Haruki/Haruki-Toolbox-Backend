from quart import Quart

from Api.mysekai import mysekai_api
from Api.suite import suite_api
from Api.public_api import public_api
from Api.private_api import private_api
from Api.inherit import inherit_api

app = Quart(__name__)
app.register_blueprint(mysekai_api)
app.register_blueprint(suite_api)
app.register_blueprint(public_api)
app.register_blueprint(inherit_api)
app.register_blueprint(private_api)