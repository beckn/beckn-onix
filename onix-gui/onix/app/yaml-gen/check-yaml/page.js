"use client";
import { useState } from "react";
import { Ubuntu_Mono } from "next/font/google";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import InputField from "@/components/InputField/InputField";
import Slider from "@/components/Slider/Slider";
import styles from "../../page.module.css";
import { toast } from "react-toastify";
import Link from "next/link";

const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function CheckYaml() {
  const [checked, setChecked] = useState(false);
  const [propertyLink, setPropertyLink] = useState("");

  const handleYamlChange = (event) => {
    setPropertyLink(event.target.value);
  };

  const handleOnclick = async () => {
    const toastId = toast.loading("Checking for layer2 yaml file");
    try {
      const response = await fetch("/api/check-layer2", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ checked }),
      });

      if (response.ok) {
        const data = await response.json();
        const yamlFile = data;
        console.log("YAML", yamlFile);
        if (yamlFile == 0) {
          toast.update(toastId, {
            render: "No Layer 2 Config Present 🤯",
            type: "error",
            isLoading: false,
            autoClose: 5000,
          });
        } else {
          toast.update(toastId, {
            render: "Yaml File Present 👌",
            type: "success",
            isLoading: false,
            autoClose: 5000,
          });
        }
      } else {
        console.error("Failed to check yaml");
        toast.update(toastId, {
          render: "Container Not Found 🤯",
          type: "error",
          isLoading: false,
          autoClose: 5000,
        });
      }
    } catch (error) {
      console.error("An error occurred:", error);
    }
  };
  return (
    <>
      <main className={ubuntuMono.className}>
        <button
          onClick={() => window.history.back()}
          className={styles.backButton}
        >
          Back
        </button>

        <div className={styles.mainContainer}>
          <p className={styles.mainText}>
            <b>Yaml File Checker</b>
          </p>

          <div className={styles.formContainer}>
            <Slider
              label={checked ? "BPP" : "BAP"}
              checked={checked}
              toggleChecked={setChecked}
            />
            <InputField
              label={"Container Name"}
              value={checked ? "bpp-network" : "bap-network"}
            />
            {/* <InputField
              label={"Yaml  Link"}
              value={propertyLink}
              onChange={handleYamlChange}
            /> */}

            <div className={styles.buttonsContainer}>
              <PrimaryButton text={"Continue"} onClick={handleOnclick} />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
