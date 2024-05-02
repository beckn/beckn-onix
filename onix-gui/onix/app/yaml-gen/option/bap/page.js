"use client";

import InputField from "@/components/InputField/InputField";
import styles from "../../../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import SecondaryButton from "@/components/Buttons/SecondaryButton";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import { usePathname } from "next/navigation";
import { useState, useCallback } from "react";
import Slider from "@/components/Slider/Slider"

const ubuntuMono = Ubuntu_Mono({
    weight: "400",
    style: "normal",
    subsets: ["latin"],
});


export default function Bap() {

    const [checked, setChecked] = useState(false);
    const [propertyName, setPropertyName] = useState("");
    const [propertyLink, setPropertyLink] = useState("");
    const [propertyOption, setPropertyOption] = useState("");

    return (
        <>
            <main className={ubuntuMono.className}>
                <div className={styles.mainContainer}>
                    <p className={styles.mainText}>BAP</p>
                    <div className={styles.formContainer}>
                        <InputField
                            label={"Property Name"}
                            value={propertyName}
                            onChange={setPropertyName}
                        />
                        <InputField
                            label={"Property Link"}
                            value={propertyLink}
                            onChange={setPropertyLink}
                        />

                        <InputField
                            label={"Property Option"}
                            value={propertyOption}
                            onChange={setPropertyOption}
                        />

                        <Slider
                            label={"Is this property mandatory?"}
                            checked={checked}
                            toggleChecked={setChecked}
                        />

                        <div className={styles.buttonsContainer}>
                            <SecondaryButton text={"Cancel"} />
                            <PrimaryButton text={"Continue"} />
                        </div>
                    </div>
                </div>
            </main>
        </>
    )
}