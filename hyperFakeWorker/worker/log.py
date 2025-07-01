import logging


def setup_logger(log_level: str, log_file: str | None):
    global logger
    
    level = logging.INFO
    match log_level.lower():
        case "debug":
            level = logging.DEBUG
        case "info":
            level = logging.INFO
        case "warn" | "warning":
            level = logging.WARNING
        case "error":
            level = logging.ERROR
        case "critical":
            level = logging.CRITICAL

    kwargs = {
        "level": level,
        "format": "[%(levelname)s] %(asctime)s - %(module)s: %(message)s"
    }
    if log_file:
        kwargs["filename"] = log_file

    logging.basicConfig(**kwargs)
    logger = logging.getLogger()
    logger.setLevel(level)
