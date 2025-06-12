from typing import Dict


async def clean_suite(suite: Dict[str, list]) -> Dict[str, list]:
    remove_keys = [
        "userActionSets",
        "userMusicAchievements",
        "userBillingShopItems",
        "userMaterials",
        "userUnitEpisodeStatuses",
        "userSpecialEpisodeStatuses",
        "userEventEpisodeStatuses",
        "userArchiveEventEpisodeStatuses",
        "userCharacterProfileEpisodeStatuses",
        "userCostume3dStatuses",
        "userCostume3dShopItems",
        "userReleaseConditions",
        "userMissionStatuses",
        "userEventExchanges",
        "userInformations",
        "userCustomProfiles",
        "userCustomProfileCards",
        "userCustomProfileResources",
        "userCustomProfileResourceUsages",
        "userCustomProfileGachas",
    ]
    for key in remove_keys:
        if key in suite:
            suite[key] = []
    return suite
