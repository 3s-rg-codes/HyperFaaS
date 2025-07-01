import datetime
from pathlib import Path
import random
from hashlib import sha256
import threading
from queue import Queue
import time
from weakref import WeakSet

from . import FunctionIdStr, InstanceIdStr
from .names import adjectives, names
from ..api.controller.controller_pb2 import StatusUpdate, Event, Status, FunctionState, InstanceState
from ..api.common.common_pb2 import InstanceID, FunctionID
from ..log import logger
from ..utils.time import get_timestamp
from .model import FunctionModelInferer, FunctionModelInput, FunctionModelOutput

from ..kvstore.client import KVStoreClient
from .image import FunctionImage

class Function():

    def __init__(self, manager: "FunctionManager", name: str, function_id: str, instance_id: str, image: FunctionImage, model: FunctionModelInferer):
        self.manager = manager

        self.created_at = int(datetime.datetime.now().timestamp())
        self.last_worked_at = int(datetime.datetime.now().timestamp())

        self.work_lock: threading._RLock = threading.RLock()

        self.name = name
        self.function_id = function_id
        self.instance_id = instance_id
        self.image = image

        self.model = model

        self.cpu = 0.0
        self.ram = 0

        self.is_cold = True

    @property
    def was_recently_active(self):
        return self.is_active or self.time_since_last_work <= 1.0

    @property
    def is_active(self):
        got_lock = self.work_lock.acquire(blocking=False)
        if got_lock:
            self.work_lock.release()
        return not got_lock

    @property
    def uptime(self):
        current_time = int(datetime.datetime.now().timestamp())
        return current_time - self.created_at
    
    @property
    def time_since_last_work(self):
        current_time = int(datetime.datetime.now().timestamp())
        return current_time - self.last_worked_at

    @staticmethod
    def create_new(manager: "FunctionManager", function_id: str, image: FunctionImage, model: Path):
        if model is None:
            raise ValueError(f"model cannot be None!")
        hash_source = function_id + str(random.randint(1, 2**31))
        return Function(
            manager=manager,
            name=f"{random.choice(adjectives)}-{random.choice(names)}",
            function_id=function_id,
            instance_id=sha256(hash_source.encode(errors="ignore")).hexdigest()[0:12],
            image=image,
            model=FunctionModelInferer(model)
        )
    
    def coldstart(self):
        logger.debug(f"Waiting for coldstart of function {self.function_id} - {self.instance_id}")
        # time.sleep(1)
        self.is_cold = False
        self.manager.send_status_update(update=StatusUpdate(
            instance_id=InstanceID(id=self.instance_id),
            event=Event.Value("EVENT_RUNNING"),
            status=Status.Value("STATUS_SUCCESS"),
            function_id=FunctionID(id=self.function_id),
            timestamp=get_timestamp()
        ))
        logger.debug(f"Finished coldstart of function {self.function_id} - {self.instance_id}")

    def timeout(self):
        self.manager.send_status_update(update=StatusUpdate(
            instance_id=InstanceID(id=self.instance_id),
            event=Event.Value("EVENT_TIMEOUT"),
            function_id=FunctionID(id=self.function_id),
        ))
        return b''

    def work(self, body_size: int, bytes: int):
        with self.work_lock:
            self.last_worked_at = int(datetime.datetime.now().timestamp())
            logger.debug(f"Executing function {self.function_id} - {self.instance_id}")
            if self.is_cold:
                self.coldstart()
            results = self.model.infer(
                FunctionModelInput(body_size, 
                                   self.manager.num_functions[self.function_id], 
                                   self.manager.num_active_functions[self.function_id], 
                                   self.manager.total_cpu_usage, 
                                   self.manager.total_ram_usage
                                   )
            )
            self.cpu = results.cpu_usage
            self.ram = results.ram_usage
            time.sleep(results.function_runtime)
            timeout = False
            self.last_worked_at = int(datetime.datetime.now().timestamp())
            if timeout:
                return self.timeout()
            self.cpu = 0
            self.ram = 0
            return random.randbytes(bytes), results.function_runtime
           
    def __eq__(self, value):
        if not isinstance(value, Function):
            return False
        return self.function_id == value.function_id and self.instance_id == value.instance_id
    
    def __hash__(self):
        return self.instance_id.__hash__()

class FunctionManager():

    def __init__(self, models: list[Path], db_address: str, update_buffer_size: int):
        self.function_lock = threading.RLock()
        # instance_id : Function
        self.active_functions: dict[InstanceIdStr, Function] = {}
        # function_id : set[Function]
        self.instances: dict[FunctionIdStr, set[Function]] = {}
        self.images: dict[FunctionIdStr, FunctionImage] = {}

        self.function_model_paths = models
        self.function_models: dict[FunctionIdStr, Path] = {}

        self.kvs_client = KVStoreClient(db_address)
        
        self.status_lock = threading.RLock()
        self.status_queues: WeakSet[Queue] = WeakSet()
        
        self.update_buffer_size = update_buffer_size

    @property
    def total_cpu_usage(self) -> float:
        with self.function_lock:
            return sum([func.cpu for func in self.active_functions.values()], 0)
    
    @property
    def total_ram_usage(self) -> int:
        with self.function_lock:
            return sum([func.ram for func in self.active_functions.values()], 0)

    def get_image(self, function_id: FunctionIdStr):
        with self.function_lock:
            if self.images.get(function_id) is None:
                self.images[function_id] = self.kvs_client.get_image(function_id)
            return self.images[function_id]

    def get_num_recently_active_functions(self, function_id: FunctionIdStr) -> int:
        with self.function_lock:
            return len(list(filter(lambda i: i.was_recently_active, self.instances.get(function_id))))

    @property
    def num_recently_active_functions(self) -> dict[FunctionIdStr, int]:
        with self.function_lock:
            active_funcs = {}
            for key, value in self.instances.items():
                active = list(filter(lambda i: i.was_recently_active, value))
                active_funcs[key] = len(active)
            return active_funcs

    def get_num_active_functions(self, function_id: FunctionIdStr) -> int:
        with self.function_lock:
            return len(list(filter(lambda i: i.is_active, self.instances.get(function_id))))

    @property
    def num_active_functions(self) -> dict[FunctionIdStr, int]:
        with self.function_lock:
            active_funcs = {}
            for key, value in self.instances.items():
                active = list(filter(lambda i: i.is_active, value))
                active_funcs[key] = len(active)
            return active_funcs
    
    def get_num_functions(self, function_id: FunctionIdStr) -> int:
        with self.function_lock:
            return len(self.instances.get(function_id))

    @property
    def num_functions(self) -> dict[FunctionIdStr, int]:
        with self.function_lock:
            active_funcs = {}
            for key, value in self.instances.items():
                active_funcs[key] = len(value)
            return active_funcs

    def find_model(self, function_id: FunctionIdStr, image: FunctionImage) -> Path:
        # resolve model path
        if self.function_models.get(function_id) is None:
            for p in self.function_model_paths:
                if p.stem == image.image:
                    self.function_models[function_id] = p
                    break
        return self.function_models.get(function_id)

    def add_function(self, function: Function):
        with self.function_lock:
            # Add to function instance map
            self.active_functions[function.instance_id] = function

            # Add to set of all instances of an image
            if self.instances.get(function.function_id) is None:
                self.instances[function.function_id] = set()
            self.instances[function.function_id].add(function)

    def remove_function(self, instance_id: InstanceIdStr):
        with self.function_lock:
            if self.active_functions.get(instance_id) is None:
                return
            function = self.active_functions[instance_id]
            self.instances[function.function_id].remove(function)
            return self.active_functions.pop(instance_id)

    def get_function(self, instance_id: InstanceIdStr):
        with self.function_lock:
            try:
                return self.active_functions[instance_id]
            except KeyError as e:
                logger.critical(f"Failed to find instance_id {instance_id} in:\n{self.active_functions.keys()}")
                raise e
            
    def choose_function(self, function_id: FunctionIdStr):
        with self.function_lock:
            available_functions = [func for func in self.instances[function_id] if not func.is_active]
            if len(available_functions) > 0:
                return available_functions[0]
            return None
    
    def send_status_update(self, update: StatusUpdate):
        if not isinstance(update, StatusUpdate):
            raise TypeError("The sent update must be an actual status update!")
        with self.status_lock:
            for q in self.status_queues:
                q.put(update)
            
    def get_status_updates(self):
        updates_queue: Queue[StatusUpdate] = Queue(maxsize=self.update_buffer_size)
        with self.status_lock:
            self.status_queues.add(updates_queue)
        
        try:
            while True:
                update = updates_queue.get()
                yield update
                updates_queue.task_done()
        finally:
            with self.status_lock:
                self.status_queues.remove(updates_queue)


    def get_state(self) -> list[FunctionState]:
        with self.function_lock:
            state = []
            for function_id in self.instances.keys():
                functions = self.instances[function_id]
                running = []
                idle = []
                for func in functions:
                    instance_state = InstanceState(
                        instance_id=func.instance_id,
                        is_active=func.is_active,
                        time_since_last_work=func.time_since_last_work,
                        uptime=func.uptime
                    )
                    if func.is_active:
                        running.append(
                            instance_state
                        )
                    else:
                        idle.append(
                            instance_state
                        )
                if len(idle) <= 0 and len(running) <= 0:
                    state.append(FunctionState(
                        function_id=FunctionID(id=function_id),
                    ))
                elif len(idle) > 0 and len(running) <= 0:
                    state.append(FunctionState(
                        function_id=FunctionID(id=function_id),
                        idle=idle
                    ))
                elif len(idle) > 0 and len(running) > 0:
                    state.append(FunctionState(
                        function_id=FunctionID(id=function_id),
                        running=running,
                    ))
                else:
                    state.append(FunctionState(
                        function_id=FunctionID(id=function_id),
                        running=running,
                        idle=idle
                    ))
            return state